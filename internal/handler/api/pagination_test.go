package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler/api"
)

func seedEnvironments(t *testing.T, h *testHarness, n int) []db.Environment {
	t.Helper()
	out := make([]db.Environment, n)
	for i := 0; i < n; i++ {
		e, err := h.repo.Queries.CreateEnvironment(
			context.Background(),
			db.CreateEnvironmentParams{
				Name:        fmt.Sprintf("env-%d", i),
				Description: sql.NullString{},
				Tags:        sql.NullString{},
			},
		)
		if err != nil {
			t.Fatalf("create env %d: %v", i, err)
		}
		out[i] = e
	}
	return out
}

func seedLifecycles(t *testing.T, h *testHarness, n int) []db.Lifecycle {
	t.Helper()
	out := make([]db.Lifecycle, n)
	for i := 0; i < n; i++ {
		lc, err := h.repo.Queries.CreateLifecycle(
			context.Background(),
			db.CreateLifecycleParams{
				Name:        fmt.Sprintf("lc-%d", i),
				Description: sql.NullString{},
			},
		)
		if err != nil {
			t.Fatalf("create lc %d: %v", i, err)
		}
		out[i] = lc
	}
	return out
}

func seedSteps(t *testing.T, h *testHarness, projectID int64, n int) []db.Step {
	t.Helper()
	out := make([]db.Step, n)
	for i := 0; i < n; i++ {
		s, err := h.repo.Queries.CreateStep(
			context.Background(),
			db.CreateStepParams{
				ProjectID:      projectID,
				Name:           fmt.Sprintf("step-%d", i),
				ScriptBody:     "echo hi",
				SortOrder:      int64(i + 1),
				TimeoutSeconds: 60,
				MaxRetries:     0,
			},
		)
		if err != nil {
			t.Fatalf("create step %d: %v", i, err)
		}
		out[i] = s
	}
	return out
}

func seedVariables(
	t *testing.T,
	h *testHarness,
	projectID int64,
	n int,
	envID int64,
	secret bool,
) []db.Variable {
	t.Helper()
	out := make([]db.Variable, n)
	for i := 0; i < n; i++ {
		var envParam sql.NullInt64
		if envID > 0 {
			envParam = sql.NullInt64{Int64: envID, Valid: true}
		}
		secretVal := int64(0)
		if secret {
			secretVal = 1
		}
		v, err := h.repo.Queries.CreateVariable(
			context.Background(),
			db.CreateVariableParams{
				ProjectID:     projectID,
				Name:          fmt.Sprintf("var-%d", i),
				Value:         sql.NullString{String: "v", Valid: true},
				EnvironmentID: envParam,
				Secret:        secretVal,
			},
		)
		if err != nil {
			t.Fatalf("create var %d: %v", i, err)
		}
		out[i] = v
	}
	return out
}

func seedReleases(
	t *testing.T,
	h *testHarness,
	projectID int64,
	n int,
) []db.Release {
	t.Helper()
	out := make([]db.Release, n)
	for i := 0; i < n; i++ {
		r, err := h.repo.Queries.CreateRelease(
			context.Background(),
			db.CreateReleaseParams{
				ProjectID: projectID,
				Version:   fmt.Sprintf("v%d.0.0", i),
				StepsJson: "[]",
			},
		)
		if err != nil {
			t.Fatalf("create release %d: %v", i, err)
		}
		out[i] = r
	}
	return out
}

func seedSchedules(
	t *testing.T,
	h *testHarness,
	projectID, releaseID, envID int64,
	n int,
) []db.ScheduledDeployment {
	t.Helper()
	out := make([]db.ScheduledDeployment, n)
	for i := 0; i < n; i++ {
		s, err := h.repo.Queries.CreateScheduledDeployment(
			context.Background(),
			db.CreateScheduledDeploymentParams{
				ProjectID:     projectID,
				ReleaseID:     releaseID,
				EnvironmentID: envID,
				Cron:          "0 0 * * *",
				NextRunAt:     0,
				Enabled:       1,
			},
		)
		if err != nil {
			t.Fatalf("create sched %d: %v", i, err)
		}
		out[i] = s
	}
	return out
}

func callAPI(
	method, path, token string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// --- Pagination param validation ---

func TestPagination_LimitTooLarge(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/environments?limit=5000",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestPagination_LimitZero(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/environments?limit=0",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestPagination_LimitNegative(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/environments?limit=-1",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestPagination_OffsetNegative(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/environments?offset=-1",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestPagination_OffsetNotInt(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/environments?offset=abc",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestPagination_LimitNotInt(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/environments?limit=abc",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

// --- Total = true count, not page size ---

func TestPagination_TotalIsTrueCount(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	seedEnvironments(t, h, 5)

	envH := api.NewEnvironmentHandler(h.repo)
	rec := callAPI(
		http.MethodGet,
		"/api/v1/environments?limit=2&offset=0",
		token,
		envH.ListEnvironments,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Items  []map[string]any `json:"items"`
		Total  int64            `json:"total"`
		Limit  int64            `json:"limit"`
		Offset int64            `json:"offset"`
	}
	mustDecode(t, rec.Body, &env)
	if env.Total != 5 {
		t.Fatalf("expected total=5, got %d", env.Total)
	}
	if env.Limit != 2 {
		t.Fatalf("expected limit=2, got %d", env.Limit)
	}
	if len(env.Items) != 2 {
		t.Fatalf("expected 2 items in page, got %d", len(env.Items))
	}
}

func TestPagination_Environments(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	seedEnvironments(t, h, 7)

	envH := api.NewEnvironmentHandler(h.repo)
	rec := callAPI(
		http.MethodGet,
		"/api/v1/environments",
		token,
		envH.ListEnvironments,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	list := decodeList(t, rec)
	if len(list) != 7 {
		t.Fatalf("expected 7 envs, got %d", len(list))
	}

	// Walk pages of 3 — should yield 7 unique names (the 3rd page
	// has 1 remaining). Total is 7 across all pages.
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		rec := callAPI(
			http.MethodGet,
			fmt.Sprintf(
				"/api/v1/environments?limit=3&offset=%d",
				page*3,
			),
			token,
			envH.ListEnvironments,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: %d", page, rec.Code)
		}
		pageList := decodeList(t, rec)
		for _, e := range pageList {
			seen[e["name"].(string)] = true
		}
	}
	if len(seen) != 7 {
		t.Fatalf("expected 7 unique names across pages, got %d", len(seen))
	}
}

func TestPagination_Lifecycles(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	seedLifecycles(t, h, 4)

	lcH := api.NewLifecycleHandler(h.repo)
	rec := callAPI(
		http.MethodGet,
		"/api/v1/lifecycles?limit=2",
		token,
		lcH.ListLifecycles,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	list := decodeList(t, rec)
	if len(list) != 2 {
		t.Fatalf("expected 2 lifecycles, got %d", len(list))
	}
}

func TestPagination_Templates(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	for i := 0; i < 3; i++ {
		h.seedTemplate(t, fmt.Sprintf("tpl-%d", i))
	}

	tplH := api.NewStepTemplateHandler(h.repo)
	rec := callAPI(
		http.MethodGet,
		"/api/v1/templates?limit=10",
		token,
		tplH.ListTemplates,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	list := decodeList(t, rec)
	if len(list) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(list))
	}
}

func TestPagination_Users(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	h.seedUser(t, "u1@example.com", "viewer")
	h.seedUser(t, "u2@example.com", "viewer")
	h.seedUser(t, "u3@example.com", "viewer")

	userH := api.NewUsersHandler(h.repo)
	rec := callAPI(
		http.MethodGet,
		"/api/v1/users?limit=2",
		token,
		userH.ListUsers,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	list := decodeList(t, rec)
	if len(list) != 2 {
		t.Fatalf("expected 2 users, got %d", len(list))
	}
}

func TestPagination_Projects_RoleBranch(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	other := h.seedUser(t, "other@example.com", "deployer")

	p1, err := h.repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{
			Name: "p1", Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	p2, err := h.repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{
			Name: "p2", Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: p1.ID, UserID: admin.ID, Role: "admin",
		},
	); err != nil {
		t.Fatalf("add admin to p1: %v", err)
	}
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: p2.ID, UserID: admin.ID, Role: "admin",
		},
	); err != nil {
		t.Fatalf("add admin to p2: %v", err)
	}
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: p2.ID, UserID: other.ID, Role: "deployer",
		},
	); err != nil {
		t.Fatalf("add other to p2: %v", err)
	}

	projH := api.NewProjectHandler(h.repo)

	// Direct call — middleware isn't running, so stash the user
	// in the request context ourselves (callAPI only sets the
	// Authorization header, which the handler doesn't read).
	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	adminReq = withAPIUser(adminReq, admin)
	recAdmin := httptest.NewRecorder()
	projH.ListProjects(recAdmin, adminReq)
	if recAdmin.Code != http.StatusOK {
		t.Fatalf("admin list: %d", recAdmin.Code)
	}
	list := decodeList(t, recAdmin)
	if len(list) != 2 {
		t.Fatalf("admin should see 2 projects, got %d", len(list))
	}

	otherReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	otherReq = withAPIUser(otherReq, other)
	recOther := httptest.NewRecorder()
	projH.ListProjects(recOther, otherReq)
	if recOther.Code != http.StatusOK {
		t.Fatalf("other list: %d", recOther.Code)
	}
	list = decodeList(t, recOther)
	if len(list) != 1 {
		t.Fatalf("non-admin should see 1 project, got %d", len(list))
	}
	if list[0]["id"] != float64(p2.ID) {
		t.Fatalf("non-admin should see p2, got id %v", list[0]["id"])
	}
}

func TestPagination_ProjectSteps(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	seedSteps(t, h, p.ID, 5)

	stepH := api.NewStepHandler(h.repo)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/steps?limit=2", p.ID), nil)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()
	stepH.ListSteps(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 2 {
		t.Fatalf("expected 2 steps in page, got %d", len(list))
	}
}

func TestPagination_ProjectReleases(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	seedReleases(t, h, p.ID, 3)

	relH := api.NewReleaseHandler(h.repo)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/releases", p.ID), nil)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	req = withAPIURLParam(req, "id", strconv.FormatInt(p.ID, 10))
	rec := httptest.NewRecorder()
	relH.ListReleases(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 3 {
		t.Fatalf("expected 3 releases, got %d", len(list))
	}
}

func TestPagination_ProjectSchedules(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	env := h.seedEnvironment(t, "prod")
	rels := seedReleases(t, h, p.ID, 1)
	seedSchedules(t, h, p.ID, rels[0].ID, env.ID, 2)

	schedH := api.NewScheduleHandler(h.repo)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/schedules", p.ID), nil)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	req = withAPIURLParam(req, "id", strconv.FormatInt(p.ID, 10))
	rec := httptest.NewRecorder()
	schedH.ListSchedules(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(list))
	}
}

func TestPagination_ProjectVariables_FilterByEnvironment(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	env1 := h.seedEnvironment(t, "env-1")
	env2 := h.seedEnvironment(t, "env-2")
	seedVariables(t, h, p.ID, 3, env1.ID, false)
	seedVariables(t, h, p.ID, 2, env2.ID, false)

	varH := api.NewVariableHandler(h.repo)

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"/api/v1/projects/%d/variables?environment_id=%d",
			p.ID,
			env1.ID,
		),
		nil,
	)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()
	varH.ListVariables(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 3 {
		t.Fatalf("expected 3 vars in env1, got %d", len(list))
	}

	req = httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/variables", p.ID), nil)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	rec = httptest.NewRecorder()
	varH.ListVariables(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list = decodeList(t, rec)
	if len(list) != 5 {
		t.Fatalf("expected 5 vars total, got %d", len(list))
	}
}

func TestPagination_ProjectVariables_FilterBySecretOnly(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	seedVariables(t, h, p.ID, 2, 0, false)
	seedVariables(t, h, p.ID, 3, 0, true)

	varH := api.NewVariableHandler(h.repo)
	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"/api/v1/projects/%d/variables?secret_only=true",
			p.ID,
		),
		nil,
	)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()
	varH.ListVariables(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 3 {
		t.Fatalf("expected 3 secret vars, got %d", len(list))
	}
}

func TestPagination_ProjectMembers(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	for i := 0; i < 4; i++ {
		u := h.seedUser(t, fmt.Sprintf("u%d@example.com", i), "viewer")
		if err := h.repo.Queries.AddProjectMember(
			context.Background(),
			db.AddProjectMemberParams{
				ProjectID: p.ID, UserID: u.ID, Role: "deployer",
			},
		); err != nil {
			t.Fatalf("add member %d: %v", i, err)
		}
	}

	memH := api.NewProjectMemberHandler(h.repo)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/members?limit=2", p.ID), nil)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	req = withAPIURLParam(req, "id", strconv.FormatInt(p.ID, 10))
	rec := httptest.NewRecorder()
	memH.ListMembers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 2 {
		t.Fatalf("expected 2 members in page, got %d", len(list))
	}
}

func TestPagination_Deployments_FilterByEnvAndStatus(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	env1 := h.seedEnvironment(t, "env-1")
	env2 := h.seedEnvironment(t, "env-2")
	rels := seedReleases(t, h, p.ID, 1)

	for i := 0; i < 3; i++ {
		_, err := h.repo.Queries.CreateDeployment(
			context.Background(),
			db.CreateDeploymentParams{
				ReleaseID:     rels[0].ID,
				EnvironmentID: env1.ID,
				Status:        "pending",
			},
		)
		if err != nil {
			t.Fatalf("create dep env1/pending %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		_, err := h.repo.Queries.CreateDeployment(
			context.Background(),
			db.CreateDeploymentParams{
				ReleaseID:     rels[0].ID,
				EnvironmentID: env2.ID,
				Status:        "succeeded",
			},
		)
		if err != nil {
			t.Fatalf("create dep env2/succeeded %d: %v", i, err)
		}
	}

	depH := api.NewDeploymentHandler(h.repo, nil)

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"/api/v1/projects/%d/deployments?env_id=%d",
			p.ID,
			env1.ID,
		),
		nil,
	)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()
	depH.ListDeployments(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 3 {
		t.Fatalf("expected 3 deployments in env1, got %d", len(list))
	}

	req = httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"/api/v1/projects/%d/deployments?status=succeeded",
			p.ID,
		),
		nil,
	)
	req = withAPIUser(req, admin)
	req = withAPIProjectID(req, p.ID)
	rec = httptest.NewRecorder()
	depH.ListDeployments(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list = decodeList(t, rec)
	if len(list) != 2 {
		t.Fatalf("expected 2 succeeded deployments, got %d", len(list))
	}
}

func TestAdmin_AuditLogFilters(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects",
		token,
		`{"name":"p1"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	rec = h.request(
		t,
		http.MethodPost,
		"/api/v1/environments",
		token,
		`{"name":"e1"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)

	// Action names follow the pattern "<verb>_<entity>" (see
	// internal/audit/audit.go actionMap). Filter for project creates.
	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/audit?action=create_project",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	entries := decodeList(t, rec)
	if len(entries) == 0 {
		t.Fatal("expected at least 1 audit entry with action=create_project")
	}
	for _, e := range entries {
		if e["action"] != "create_project" {
			t.Fatalf(
				"expected all entries to have action=create_project, got %v",
				e["action"],
			)
		}
	}

	rec = h.request(t, http.MethodGet, "/api/v1/admin/audit?limit=1", token, "")
	h.assertStatus(t, rec, http.StatusOK)
	entries = decodeList(t, rec)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with limit=1, got %d", len(entries))
	}

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/audit?limit=abc",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/audit?limit=2000",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/admin/audit?user_id=not-an-int",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestPagination_Tokens(t *testing.T) {
	h := newHarness(t)
	u := h.seedUser(t, "owner@example.com", "admin")
	h.seedToken(t, u)

	tokH := api.NewAPITokenHandler(h.repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens?limit=2", nil)
	req = withAPIUser(req, u)
	rec := httptest.NewRecorder()
	tokH.ListTokens(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 token (the seed), got %d", len(list))
	}
}

// Keep the strconv import used; future test expansions may use it.
var _ = strconv.Itoa
