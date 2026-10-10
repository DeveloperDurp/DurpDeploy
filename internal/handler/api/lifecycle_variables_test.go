package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type lifecycleAPI struct {
	h           *harness
	router      http.Handler
	lifecycle   db.Lifecycle
	project     db.Project
	environment db.Environment
	token       string
}

func newLifecycleAPI(t *testing.T) lifecycleAPI {
	t.Helper()
	h := newAPIHarness(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h.repo.SetSecretBox(box)
	user := seedAPIUser(t, h.repo, "lifecycle-admin@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	env := seedEnv(t, h.repo)
	lifecycle, err := h.repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "Shared lifecycle"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Queries.CreateLifecycleStage(t.Context(), db.CreateLifecycleStageParams{LifecycleID: lifecycle.ID, EnvironmentID: env.ID, SortOrder: 1}); err != nil {
		t.Fatal(err)
	}
	if err := h.repo.Queries.SetProjectLifecycle(t.Context(), db.SetProjectLifecycleParams{ID: project.ID, LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(
		h.repo,
		h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo),
	)
	return lifecycleAPI{
		h:           h,
		lifecycle:   lifecycle,
		project:     project,
		environment: env,
		token:       token,
		router:      router,
	}
}

func (f lifecycleAPI) request(
	t *testing.T,
	method, path, body string,
	status int,
) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	f.router.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf(
			"%s %s: status=%d want=%d body=%s",
			method,
			path,
			w.Code,
			status,
			w.Body.String(),
		)
	}
	return w
}

func TestLifecycleVariableAPI_CRUDAndBoundaries(t *testing.T) {
	f := newLifecycleAPI(t)
	path := fmt.Sprintf("/api/v1/lifecycles/%d/variables", f.lifecycle.ID)
	created := f.request(
		t,
		"POST",
		path,
		`{"name":"TOKEN","value":"sentinel-secret","secret":true}`,
		201,
	)
	if strings.Contains(created.Body.String(), "sentinel-secret") {
		t.Fatal("create exposes secret")
	}
	var variable struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &variable); err != nil {
		t.Fatal(err)
	}
	item := fmt.Sprintf("%s/%d", path, variable.ID)
	f.request(t, "POST", path, `{"name":"TOKEN","value":"duplicate"}`, 422)
	for _, body := range []string{`{"name":"bad name"}`, `{"name":"ARTIFACT_PATH"}`, `{"name":"DURPDEPLOY_STAGE_DIR"}`} {
		f.request(t, "POST", path, body, 422)
	}
	f.request(
		t,
		"POST",
		path,
		`{"name":"OUTSIDE","environment_id":999999}`,
		422,
	)
	f.request(t, "POST", path, `{"name":"INVALID","environment_id":0}`, 400)
	f.request(t, "POST", path, `{"name":`, 400)
	f.request(t, "POST", path, `[]`, 400)
	f.request(
		t,
		"POST",
		path,
		fmt.Sprintf(`{"name":%q}`, strings.Repeat("A", 256)),
		422,
	)
	f.request(t, "PUT", item, `{"name":"TOKEN","value":"","secret":true}`, 200)
	saved, err := f.h.repo.GetLifecycleVariable(
		t.Context(),
		db.GetLifecycleVariableParams{
			ID:          variable.ID,
			LifecycleID: f.lifecycle.ID,
		},
	)
	if err != nil || saved.Value.String != "sentinel-secret" {
		t.Fatalf("blank-secret update: %v %v", saved, err)
	}
	raw, err := f.h.repo.Queries.GetLifecycleVariable(
		t.Context(),
		db.GetLifecycleVariableParams{
			ID:          variable.ID,
			LifecycleID: f.lifecycle.ID,
		},
	)
	if err != nil || raw.Value.String == "sentinel-secret" {
		t.Fatal("value not encrypted at rest", err)
	}
	for _, url := range []string{path, item, fmt.Sprintf("/api/v1/projects/%d/variables/inherited", f.project.ID)} {
		read := f.request(t, "GET", url, "", 200)
		if strings.Contains(read.Body.String(), "sentinel-secret") {
			t.Fatal("read exposes secret", url)
		}
	}
	other, err := f.h.repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "Other lifecycle"},
	)
	if err != nil {
		t.Fatal(err)
	}
	foreign := fmt.Sprintf(
		"/api/v1/lifecycles/%d/variables/%d",
		other.ID,
		variable.ID,
	)
	f.request(t, "GET", foreign, "", 404)
	f.request(t, "PUT", foreign, `{"name":"TOKEN","value":"wrong"}`, 404)
	f.request(t, "DELETE", foreign, "", 404)
	f.request(t, "DELETE", item, "", 204)
	f.request(t, "GET", item, "", 404)
	var audited int
	if err := f.h.repo.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action IN ('create_lifecycle_variable','update_lifecycle_variable','delete_lifecycle_variable')`).Scan(&audited); err != nil ||
		audited != 3 {
		t.Fatalf("audit count=%d err=%v", audited, err)
	}
}
