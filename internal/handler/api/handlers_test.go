package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

type testHarness struct {
	repo   *repository.Repository
	router *chi.Mux
}

func newHarness(t *testing.T) *testHarness {
	dir, err := os.MkdirTemp("", "api-test-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dsn := fmt.Sprintf(
		"file:%s/test.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		dir,
	)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repo := repository.New(conn)
	_ = runner.NewLogBroker()

	r := chi.NewRouter()
	r.Use(handler.PanicRecoveryMiddleware)
	r.Group(func(ar chi.Router) {
		ar.Use(auth.ApiTokenMiddleware(repo))
		ar.Use(auth.WriteBlockMiddleware())
		ar.Use(audit.Middleware(repo))

		apiProjH := api.NewProjectHandler(repo)
		ar.Get("/api/v1/projects", apiProjH.ListProjects)
		ar.Post("/api/v1/projects", apiProjH.CreateProject)

		apiEnvH := api.NewEnvironmentHandler(repo)
		ar.Get("/api/v1/environments", apiEnvH.ListEnvironments)
		ar.Post("/api/v1/environments", apiEnvH.CreateEnvironment)
		ar.Get("/api/v1/environments/{id}", apiEnvH.GetEnvironment)
		ar.Put("/api/v1/environments/{id}", apiEnvH.UpdateEnvironment)
		ar.Delete("/api/v1/environments/{id}", apiEnvH.DeleteEnvironment)

		apiLcH := api.NewLifecycleHandler(repo)
		ar.Get("/api/v1/lifecycles", apiLcH.ListLifecycles)
		ar.Post("/api/v1/lifecycles", apiLcH.CreateLifecycle)
		ar.Get("/api/v1/lifecycles/{id}", apiLcH.GetLifecycle)
		ar.Post("/api/v1/lifecycles/{id}/save", apiLcH.SaveLifecycle)
		ar.Post("/api/v1/lifecycles/{id}/stages", apiLcH.AddStage)
		ar.Post("/api/v1/lifecycles/{id}/stages/reorder", apiLcH.ReorderStages)
		ar.Patch("/api/v1/lifecycles/{id}/stages/{stageId}", apiLcH.UpdateStage)
		ar.Post(
			"/api/v1/lifecycles/{id}/stages/{stageId}/delete",
			apiLcH.DeleteStage,
		)

		apiTplH := api.NewStepTemplateHandler(repo)
		ar.Get("/api/v1/templates", apiTplH.ListTemplates)
		ar.Post("/api/v1/templates", apiTplH.CreateTemplate)
		ar.Get("/api/v1/templates/{id}", apiTplH.GetTemplate)
		ar.Put("/api/v1/templates/{id}", apiTplH.UpdateTemplate)
		ar.Delete("/api/v1/templates/{id}", apiTplH.DeleteTemplate)
		ar.Get("/api/v1/templates/{id}/history", apiTplH.ListTemplateHistory)

		ar.Group(func(aar chi.Router) {
			aar.Use(auth.RequireRole("admin"))

			adminH := api.NewAdminHandler(repo)
			aar.Get("/api/v1/admin/notifications", adminH.ListNotifications)
			aar.Get(
				"/api/v1/admin/notifications/settings",
				adminH.GetNotificationSettings,
			)
			aar.Put(
				"/api/v1/admin/notifications/settings",
				adminH.UpdateNotificationSettings,
			)
			aar.Get("/api/v1/admin/audit", adminH.ListAuditLogs)
			aar.Get("/api/v1/admin/db-tables", adminH.DbTables)

			usersH := api.NewUsersHandler(repo)
			aar.Get("/api/v1/admin/users", usersH.ListUsers)
			aar.Post("/api/v1/admin/users", usersH.CreateUser)
			aar.Get("/api/v1/admin/users/{id}", usersH.GetUser)
			aar.Put("/api/v1/admin/users/{id}", usersH.UpdateUser)
			aar.Delete("/api/v1/admin/users/{id}", usersH.DeleteUser)
		})

		ar.Group(func(par chi.Router) {
			par.Use(auth.RequireProjectAccess(repo))

			par.Get("/api/v1/projects/{id}", apiProjH.GetProject)
			par.Put("/api/v1/projects/{id}", apiProjH.UpdateProject)
			par.Delete("/api/v1/projects/{id}", apiProjH.DeleteProject)
			par.Get(
				"/api/v1/projects/{id}/notifications",
				apiProjH.GetProjectNotifications,
			)
			par.Put(
				"/api/v1/projects/{id}/notifications",
				apiProjH.UpdateProjectNotifications,
			)

			apiStepH := api.NewStepHandler(repo)
			par.Get("/api/v1/projects/{id}/steps", apiStepH.ListSteps)
			par.Post("/api/v1/projects/{id}/steps", apiStepH.CreateStep)
			par.Get("/api/v1/projects/{id}/steps/{stepId}", apiStepH.GetStep)
			par.Put("/api/v1/projects/{id}/steps/{stepId}", apiStepH.UpdateStep)
			par.Delete(
				"/api/v1/projects/{id}/steps/{stepId}",
				apiStepH.DeleteStep,
			)
			par.Patch(
				"/api/v1/projects/{id}/steps/reorder",
				apiStepH.ReorderSteps,
			)

			apiVarH := api.NewVariableHandler(repo)
			par.Get("/api/v1/projects/{id}/variables", apiVarH.ListVariables)
			par.Post("/api/v1/projects/{id}/variables", apiVarH.CreateVariable)
			par.Get(
				"/api/v1/projects/{id}/variables/{varId}",
				apiVarH.GetVariable,
			)
			par.Put(
				"/api/v1/projects/{id}/variables/{varId}",
				apiVarH.UpdateVariable,
			)
			par.Delete(
				"/api/v1/projects/{id}/variables/{varId}",
				apiVarH.DeleteVariable,
			)

			par.Get(
				"/api/v1/projects/{id}/templates-picker",
				apiTplH.TemplatesPicker,
			)

			apiMemberH := api.NewProjectMemberHandler(repo)
			par.Get("/api/v1/projects/{id}/members", apiMemberH.ListMembers)
			par.Post("/api/v1/projects/{id}/members", apiMemberH.AddMember)
			par.Delete(
				"/api/v1/projects/{id}/members/{userId}",
				apiMemberH.RemoveMember,
			)
		})
	})

	return &testHarness{repo: repo, router: r}
}

func (h *testHarness) seedUser(t *testing.T, email, role string) *db.User {
	pwHash, err := auth.HashPassword("testpass123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := h.repo.Queries.CreateUser(
		context.Background(),
		db.CreateUserParams{
			Email:        email,
			PasswordHash: pwHash,
			Name:         "Test User",
			Role:         role,
		},
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &u
}

func (h *testHarness) seedToken(t *testing.T, user *db.User) string {
	full, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	id := uuid.NewString()
	if _, err := h.repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID:          id,
			UserID:      user.ID,
			Name:        "test token",
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
			ExpiresAt:   sql.NullInt64{Valid: false},
		},
	); err != nil {
		t.Fatalf("create api token: %v", err)
	}
	return full
}

func (h *testHarness) seedProject(t *testing.T, user *db.User) db.Project {
	p, err := h.repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{
			Name:        "test-project",
			Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: p.ID,
			UserID:    user.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatalf("add project member: %v", err)
	}
	return p
}

func (h *testHarness) seedEnvironment(
	t *testing.T,
	name string,
) db.Environment {
	e, err := h.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{
			Name:        name,
			Description: sql.NullString{},
			Tags:        sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	return e
}

func (h *testHarness) seedLifecycle(t *testing.T, name string) db.Lifecycle {
	lc, err := h.repo.Queries.CreateLifecycle(
		context.Background(),
		db.CreateLifecycleParams{
			Name:        name,
			Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	return lc
}

func (h *testHarness) seedStep(t *testing.T, projectID int64) db.Step {
	s, err := h.repo.Queries.CreateStep(
		context.Background(),
		db.CreateStepParams{
			ProjectID:      projectID,
			Name:           "step-one",
			ScriptBody:     "echo 1",
			SortOrder:      1,
			TimeoutSeconds: 60,
			MaxRetries:     0,
		},
	)
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	return s
}

func (h *testHarness) seedTemplate(t *testing.T, name string) db.StepTemplate {
	tpl, err := h.repo.Queries.CreateStepTemplate(
		context.Background(),
		db.CreateStepTemplateParams{
			Name:       name,
			ScriptBody: "echo tpl",
		},
	)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if _, err := h.repo.Queries.CreateStepTemplateVersion(
		context.Background(),
		db.CreateStepTemplateVersionParams{
			TemplateID:    tpl.ID,
			VersionNumber: 1,
			Name:          tpl.Name,
			ScriptBody:    tpl.ScriptBody,
		},
	); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	return tpl
}

func (h *testHarness) seedVariable(
	t *testing.T,
	projectID int64,
	name string,
) db.Variable {
	v, err := h.repo.Queries.CreateVariable(
		context.Background(),
		db.CreateVariableParams{
			ProjectID: projectID,
			Name:      name,
			Value:     sql.NullString{String: "val", Valid: true},
			Secret:    0,
		},
	)
	if err != nil {
		t.Fatalf("create variable: %v", err)
	}
	return v
}

func (h *testHarness) request(
	t *testing.T,
	method, path, token, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func (h *testHarness) assertStatus(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	want int,
) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf(
			"expected status %d, got %d: %s",
			want,
			rec.Code,
			rec.Body.String(),
		)
	}
}

func (h *testHarness) assertJSONField(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	key string,
	want any,
) {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp[key] != want {
		t.Fatalf("expected %s=%v, got %v", key, want, resp[key])
	}
}

// decodeList decodes a paginated list response and returns the items
// slice. The list endpoints now return
// {"items":[...], "total":N, "limit":L, "offset":O}; callers that
// need a concrete type should json.Unmarshal items[i] themselves.
func decodeList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var env struct {
		Items  []map[string]any `json:"items"`
		Total  int64            `json:"total"`
		Limit  int64            `json:"limit"`
		Offset int64            `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode list envelope: %v (body=%s)", err, rec.Body.String())
	}
	return env.Items
}

// decodeListBody decodes a paginated list response from a raw
// io.ReadCloser (used by tests that already consumed the body via
// json.NewDecoder).
func decodeListBody(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var env struct {
		Items  []map[string]any `json:"items"`
		Total  int64            `json:"total"`
		Limit  int64            `json:"limit"`
		Offset int64            `json:"offset"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode list envelope: %v (body=%s)", err, string(body))
	}
	return env.Items
}

func (h *testHarness) adminToken(t *testing.T) string {
	return h.seedToken(t, h.seedUser(t, "admin@example.com", "admin"))
}

func (h *testHarness) deployerToken(t *testing.T) string {
	u := h.seedUser(t, "deployer@example.com", "deployer")
	return h.seedToken(t, u)
}

func (h *testHarness) viewerToken(t *testing.T) string {
	u := h.seedUser(t, "viewer@example.com", "viewer")
	return h.seedToken(t, u)
}

func TestProject_CreateAndList(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects",
		token,
		`{"name":"api-proj"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "name", "api-proj")

	rec = h.request(t, http.MethodGet, "/api/v1/projects", token, "")
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 project, got %d", len(list))
	}
}

func TestProject_CreateRequiresName(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects",
		token,
		`{"name":""}`,
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestProject_GetUpdateDelete(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "test-project")

	rec = h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(p.ID),
		token,
		`{"name":"renamed"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "renamed")

	rec = h.request(
		t,
		http.MethodDelete,
		"/api/v1/projects/"+itoa(p.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNotFound)
}

func TestProject_UpdateNotifications(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(p.ID)+"/notifications",
		token,
		`{"slack_webhook_url":"https://example.com/hook"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["slack_webhook_url"] != "https://example.com/hook" {
		t.Fatalf("expected slack webhook, got %v", resp["slack_webhook_url"])
	}
}

func TestProject_CreateWithLifecycle(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	lc := h.seedLifecycle(t, "prod-flow")

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects",
		token,
		`{"name":"lc-proj","lifecycle_id":`+itoa(lc.ID)+`}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["lifecycle_id"] != float64(lc.ID) {
		t.Fatalf(
			"expected lifecycle_id %d, got %v",
			lc.ID,
			resp["lifecycle_id"],
		)
	}
}

func TestEnvironment_CreateAndList(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/environments",
		token,
		`{"name":"prod","tags":"prod"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "name", "prod")

	rec = h.request(t, http.MethodGet, "/api/v1/environments", token, "")
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 environment, got %d", len(list))
	}
}

func TestEnvironment_CreateDuplicate(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	h.request(
		t,
		http.MethodPost,
		"/api/v1/environments",
		token,
		`{"name":"prod"}`,
	)
	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/environments",
		token,
		`{"name":"prod"}`,
	)
	h.assertStatus(t, rec, http.StatusConflict)
}

func TestEnvironment_GetUpdateDelete(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	e := h.seedEnvironment(t, "staging")

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/environments/"+itoa(e.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "staging")

	rec = h.request(
		t,
		http.MethodPut,
		"/api/v1/environments/"+itoa(e.ID),
		token,
		`{"name":"staging-2","tags":"stage"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "staging-2")

	rec = h.request(
		t,
		http.MethodDelete,
		"/api/v1/environments/"+itoa(e.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)
}

func TestLifecycle_CreateAndGet(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles",
		token,
		`{"name":"release-flow"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "name", "release-flow")
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	id := int64(resp["id"].(float64))

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/lifecycles/"+itoa(id),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	var getResp struct {
		Lifecycle map[string]any   `json:"lifecycle"`
		Stages    []map[string]any `json:"stages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode get body: %v", err)
	}
	if getResp.Lifecycle["name"] != "release-flow" {
		t.Fatalf(
			"expected lifecycle name release-flow, got %v",
			getResp.Lifecycle["name"],
		)
	}
	if len(getResp.Stages) != 0 {
		t.Fatalf("expected 0 stages, got %d", len(getResp.Stages))
	}
}

func TestLifecycle_SaveAndStageManagement(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	lc := h.seedLifecycle(t, "flow")
	e := h.seedEnvironment(t, "prod")

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/save",
		token,
		`{"name":"flow-renamed"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	var saved map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode saved lifecycle: %v", err)
	}
	if saved["lifecycle"].(map[string]any)["name"] != "flow-renamed" {
		t.Fatalf(
			"expected lifecycle.name flow-renamed, got %v",
			saved["lifecycle"].(map[string]any)["name"],
		)
	}

	rec = h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages",
		token,
		`{"environment_id":`+itoa(
			e.ID,
		)+`,"sort_order":1,"requires_approval":true}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	var stage map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &stage); err != nil {
		t.Fatalf("decode stage: %v", err)
	}
	stageID := int64(stage["id"].(float64))

	rec = h.request(
		t,
		http.MethodPatch,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages/"+itoa(stageID),
		token,
		`{"requires_approval":false}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated stage: %v", err)
	}
	if updated["requires_approval"] != float64(0) {
		t.Fatalf(
			"expected requires_approval 0, got %v",
			updated["requires_approval"],
		)
	}

	rec = h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages/"+itoa(stageID)+"/delete",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)
}

func TestLifecycle_ReorderStages(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	lc := h.seedLifecycle(t, "flow")
	e1 := h.seedEnvironment(t, "prod")
	e2 := h.seedEnvironment(t, "stage")

	s1 := h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages",
		token,
		`{"environment_id":`+itoa(e1.ID)+`,"sort_order":1}`,
	)
	h.assertStatus(t, s1, http.StatusCreated)
	s2 := h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages",
		token,
		`{"environment_id":`+itoa(e2.ID)+`,"sort_order":2}`,
	)
	h.assertStatus(t, s2, http.StatusCreated)

	var stage1, stage2 map[string]any
	json.Unmarshal(s1.Body.Bytes(), &stage1)
	json.Unmarshal(s2.Body.Bytes(), &stage2)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages/reorder",
		token,
		`{"stage_ids":[`+itoa(
			int64(stage2["id"].(float64)),
		)+`,`+itoa(
			int64(stage1["id"].(float64)),
		)+`]}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	var stages []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &stages); err != nil {
		t.Fatalf("decode stages: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	if stages[0]["environment_id"] != float64(e2.ID) {
		t.Fatalf(
			"expected first stage env id %d, got %v",
			e2.ID,
			stages[0]["environment_id"],
		)
	}
}

func TestLifecycle_AddStageDuplicateEnvironment(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	lc := h.seedLifecycle(t, "flow")
	e := h.seedEnvironment(t, "prod")
	h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages",
		token,
		`{"environment_id":`+itoa(e.ID)+`}`,
	)
	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/lifecycles/"+itoa(lc.ID)+"/stages",
		token,
		`{"environment_id":`+itoa(e.ID)+`}`,
	)
	h.assertStatus(t, rec, http.StatusConflict)
}

func TestStep_CreateAndList(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/steps",
		token,
		`{"name":"deploy","script_body":"echo deploy"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "name", "deploy")

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID)+"/steps",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 step, got %d", len(list))
	}
}

func TestStep_GetUpdateDelete(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)
	s := h.seedStep(t, p.ID)

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID)+"/steps/"+itoa(s.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "step-one")

	rec = h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(p.ID)+"/steps/"+itoa(s.ID),
		token,
		`{"name":"updated","script_body":"echo updated","sort_order":2,"timeout_seconds":120,"max_retries":1}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "updated")

	rec = h.request(
		t,
		http.MethodDelete,
		"/api/v1/projects/"+itoa(p.ID)+"/steps/"+itoa(s.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)
}

func TestStep_Reorder(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)
	s1 := h.seedStep(t, p.ID)
	s2 := h.seedStep(t, p.ID)

	rec := h.request(
		t,
		http.MethodPatch,
		"/api/v1/projects/"+itoa(p.ID)+"/steps/reorder",
		token,
		`{"step_ids":[`+itoa(s2.ID)+`,`+itoa(s1.ID)+`]}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	var steps []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &steps); err != nil {
		t.Fatalf("decode steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0]["id"] != float64(s2.ID) {
		t.Fatalf("expected first step id %d, got %v", s2.ID, steps[0]["id"])
	}
}

func TestStep_CreateValidation(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/steps",
		token,
		`{"name":"","script_body":"x"}`,
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestTemplate_CreateAndList(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/templates",
		token,
		`{"name":"tpl","script_body":"echo tpl"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "name", "tpl")

	rec = h.request(t, http.MethodGet, "/api/v1/templates", token, "")
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 template, got %d", len(list))
	}
}

func TestTemplate_GetUpdateDelete(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	tpl := h.seedTemplate(t, "base")

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/templates/"+itoa(tpl.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "base")

	rec = h.request(
		t,
		http.MethodPut,
		"/api/v1/templates/"+itoa(tpl.ID),
		token,
		`{"name":"base-v2","script_body":"echo v2"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "base-v2")

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/templates/"+itoa(tpl.ID)+"/history",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	var history []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	rec = h.request(
		t,
		http.MethodDelete,
		"/api/v1/templates/"+itoa(tpl.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)
}

func TestTemplate_Picker(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)
	h.seedTemplate(t, "pick-me")

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID)+"/templates-picker",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode picker: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 template, got %d", len(list))
	}
}

func TestTemplate_CreateDuplicate(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	h.seedTemplate(t, "dup")
	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/templates",
		token,
		`{"name":"dup","script_body":"x"}`,
	)
	h.assertStatus(t, rec, http.StatusConflict)
}

func TestVariable_CreateAndList(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/variables",
		token,
		`{"name":"HOST","value":"localhost"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "name", "HOST")

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID)+"/variables",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 variable, got %d", len(list))
	}
}

func TestVariable_GetUpdateDelete(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)
	v := h.seedVariable(t, p.ID, "KEY")

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID)+"/variables/"+itoa(v.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "KEY")

	rec = h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(p.ID)+"/variables/"+itoa(v.ID),
		token,
		`{"name":"KEY","value":"new-val"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "value", "new-val")

	rec = h.request(
		t,
		http.MethodDelete,
		"/api/v1/projects/"+itoa(p.ID)+"/variables/"+itoa(v.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)
}

func TestVariable_CreateRequiresName(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/variables",
		token,
		`{"name":"","value":"x"}`,
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestUser_CreateAndList(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/admin/users",
		token,
		`{"email":"new@example.com","name":"New User","password":"newpass123","role":"deployer"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "email", "new@example.com")
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["one_time_password"] != "newpass123" {
		t.Fatalf(
			"expected one_time_password in response, got %v",
			resp["one_time_password"],
		)
	}
	if _, ok := resp["password_hash"]; ok {
		t.Fatal("response should not contain password_hash")
	}

	rec = h.request(t, http.MethodGet, "/api/v1/admin/users", token, "")
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) != 2 {
		t.Fatalf("expected 2 users, got %d", len(list))
	}
}

func TestUser_GetUpdateDelete(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	u := h.seedUser(t, "target@example.com", "deployer")

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/users/"+itoa(u.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "email", "target@example.com")

	rec = h.request(
		t,
		http.MethodPut,
		"/api/v1/admin/users/"+itoa(u.ID),
		token,
		`{"name":"Renamed","role":"admin"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	h.assertJSONField(t, rec, "name", "Renamed")

	rec = h.request(
		t,
		http.MethodDelete,
		"/api/v1/admin/users/"+itoa(u.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/users/"+itoa(u.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNotFound)
}

func TestUser_CreateValidation(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/admin/users",
		token,
		`{"email":"bad","name":"x","password":"short","role":"hacker"}`,
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestUser_ViewerCannotAccessAdmin(t *testing.T) {
	h := newHarness(t)
	token := h.viewerToken(t)
	rec := h.request(t, http.MethodGet, "/api/v1/admin/users", token, "")
	h.assertStatus(t, rec, http.StatusForbidden)
}

func TestAdmin_NotificationsAndSettings(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/notifications",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/notifications/settings",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)

	rec = h.request(
		t,
		http.MethodPut,
		"/api/v1/admin/notifications/settings",
		token,
		`{"slack_webhook_url":"https://hooks.slack.com/test","notify_emails":"ops@example.com"}`,
	)
	h.assertStatus(t, rec, http.StatusOK)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if resp["slack_webhook_url"] != "https://hooks.slack.com/test" {
		t.Fatalf("expected slack webhook, got %v", resp["slack_webhook_url"])
	}
	if resp["notify_emails"] != "ops@example.com" {
		t.Fatalf("expected notify_emails, got %v", resp["notify_emails"])
	}
}

func TestAdmin_AuditLogs(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	h.request(
		t,
		http.MethodPost,
		"/api/v1/environments",
		token,
		`{"name":"audited"}`,
	)

	rec := h.request(t, http.MethodGet, "/api/v1/admin/audit", token, "")
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) == 0 {
		t.Fatal("expected audit logs, got none")
	}
}

func TestAdmin_DbTables(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(t, http.MethodGet, "/api/v1/admin/db-tables", token, "")
	h.assertStatus(t, rec, http.StatusOK)

	var tables []string
	if err := json.Unmarshal(rec.Body.Bytes(), &tables); err != nil {
		t.Fatalf("decode tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("expected migrated database tables, got none")
	}
}

func TestViewer_WriteBlocked(t *testing.T) {
	h := newHarness(t)
	token := h.viewerToken(t)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects",
		token,
		`{"name":"viewer-proj"}`,
	)
	h.assertStatus(t, rec, http.StatusForbidden)

	rec = h.request(
		t,
		http.MethodPost,
		"/api/v1/environments",
		token,
		`{"name":"viewer-env"}`,
	)
	h.assertStatus(t, rec, http.StatusForbidden)
}

func TestUnauthenticated(t *testing.T) {
	h := newHarness(t)
	rec := h.request(t, http.MethodGet, "/api/v1/projects", "", "")
	h.assertStatus(t, rec, http.StatusUnauthorized)
}

func itoa(n int64) string {
	var buf bytes.Buffer
	if n < 0 {
		buf.WriteByte('-')
		n = -n
	}
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	for i := len(digits) - 1; i >= 0; i-- {
		buf.WriteByte(digits[i])
	}
	return buf.String()
}
