package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestDatabaseSecurityAPIAuthorization(t *testing.T) {
	t.Setenv(
		"DURPDEPLOY_DB",
		"postgres://private:private-password@private-host/db?sslmode=disable",
	)
	h := newAPIHarness(t)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	for _, role := range []string{"anonymous", "viewer", "deployer", "admin"} {
		t.Run(role, func(t *testing.T) {
			request, err := http.NewRequest(
				http.MethodGet,
				srv.URL+"/api/v1/admin/database-security",
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusUnauthorized
			if role != "anonymous" {
				user := seedAPIUser(
					t,
					h.repo,
					role+"@database-security.test",
					role,
				)
				_, token := seedAPIToken(t, h.repo, user.ID)
				request.Header.Set("Authorization", "Bearer "+token)
				want = http.StatusForbidden
				if role == "admin" {
					want = http.StatusOK
				}
			}
			response, err := srv.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if readErr != nil || response.StatusCode != want {
				t.Fatalf(
					"status=%d want=%d read=%v",
					response.StatusCode,
					want,
					readErr,
				)
			}
			if strings.Contains(string(body), "private") {
				t.Fatal("response exposes connection details")
			}
			if role == "admin" &&
				!strings.Contains(string(body), "plaintext_allowed") {
				t.Fatal("administrator response lacks the configured warning")
			}
		})
	}
}
