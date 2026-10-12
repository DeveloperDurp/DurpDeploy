package migrate

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/repository"
	"github.com/go-chi/chi/v5"
)

func TestLifecycleVariableDuplicateHTTPDatabaseParity(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			conn := lifecycleVariablesParityDB(t, backend)
			t.Cleanup(func() { requireNoError(t, conn.Close(), "close DB") })
			repo := repository.New(conn)
			lc, err := repo.Queries.CreateLifecycle(t.Context(),
				db.CreateLifecycleParams{Name: "HTTP duplicates"})
			requireNoError(t, err, "create lifecycle")
			env, err := repo.Queries.CreateEnvironment(t.Context(),
				db.CreateEnvironmentParams{Name: "HTTP stage"})
			requireNoError(t, err, "create environment")
			_, err = repo.Queries.CreateLifecycleStage(t.Context(),
				db.CreateLifecycleStageParams{
					LifecycleID: lc.ID, EnvironmentID: env.ID, SortOrder: 1,
				})
			requireNoError(t, err, "create stage")
			router := chi.NewRouter()
			router.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						next.ServeHTTP(
							w,
							auth.SetUser(r, &db.User{Role: "admin"}),
						)
					},
				)
			})
			router.Post("/api/v1/lifecycles/{id}/variables",
				api.NewLifecycleVariableHandler(repo).Create)
			router.Post("/lifecycles/{id}/variables",
				handler.NewLifecycleVariableHandler(repo).Save)
			srv := httptest.NewServer(router)
			t.Cleanup(srv.Close)
			for _, scoped := range []bool{false, true} {
				params := db.CreateLifecycleVariableParams{
					LifecycleID: lc.ID, Name: "REGION",
					Value: sql.NullString{String: "original", Valid: true},
				}
				body := `{"name":"REGION","value":"duplicate"}`
				form := url.Values{"name": {"REGION"}, "value": {"duplicate"}}
				if scoped {
					params.EnvironmentID = sql.NullInt64{
						Int64: env.ID,
						Valid: true,
					}
					body = fmt.Sprintf(
						`{"name":"REGION","value":"duplicate","environment_id":%d}`,
						env.ID,
					)
					form.Set("environment_id", fmt.Sprint(env.ID))
				}
				_, err := repo.Queries.CreateLifecycleVariable(
					t.Context(),
					params,
				)
				requireNoError(t, err, "seed scope")
				for _, lane := range []struct{ path, body, contentType string }{
					{fmt.Sprintf("/api/v1/lifecycles/%d/variables", lc.ID),
						body, "application/json"},
					{fmt.Sprintf("/lifecycles/%d/variables", lc.ID),
						form.Encode(), "application/x-www-form-urlencoded"},
				} {
					req, err := http.NewRequest("POST", srv.URL+lane.path,
						strings.NewReader(lane.body))
					requireNoError(t, err, "request")
					req.Header.Set("Content-Type", lane.contentType)
					req.Header.Set("HX-Request", "true")
					res, err := srv.Client().Do(req)
					requireNoError(t, err, "HTTP duplicate")
					data, err := io.ReadAll(res.Body)
					requireNoError(t, err, "response body")
					requireNoError(t, res.Body.Close(), "close body")
					if res.StatusCode != 422 || !strings.Contains(
						string(data), "name and scope already exist",
					) {
						t.Fatalf("%s scoped=%v: %d %s", lane.path,
							scoped, res.StatusCode, data)
					}
				}
			}
		})
	}
}
