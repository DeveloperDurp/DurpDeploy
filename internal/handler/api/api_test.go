package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
)

type harness struct {
	repo   *repository.Repository
	runner *runner.DeploymentRunner
	broker *runner.LogBroker
}

func newAPIHarness(t *testing.T) *harness {
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
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	return &harness{repo: repo, runner: rnr, broker: broker}
}

func seedAPIUser(
	t *testing.T,
	repo *repository.Repository,
	email, role string,
) *db.User {
	pwHash, err := auth.HashPassword("testpass")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := repo.Queries.CreateUser(context.Background(), db.CreateUserParams{
		Email:        email,
		PasswordHash: pwHash,
		Name:         "Test User",
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &u
}

func seedProject(t *testing.T, repo *repository.Repository) db.Project {
	p, err := repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{
			Name:        fmt.Sprintf("api-test-%d", time.Now().UnixNano()),
			Description: sql.NullString{String: "test", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func seedEnv(t *testing.T, repo *repository.Repository) db.Environment {
	e, err := repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{
			Name:        "prod",
			Description: sql.NullString{},
			Tags:        sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	return e
}

func seedRelease(
	t *testing.T,
	repo *repository.Repository,
	projectID int64,
) db.Release {
	r, err := repo.Queries.CreateRelease(
		context.Background(),
		db.CreateReleaseParams{
			ProjectID: projectID,
			Version:   fmt.Sprintf("1.0.%d", time.Now().UnixNano()),
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	return r
}

func seedDeployment(
	t *testing.T,
	repo *repository.Repository,
	releaseID, envID int64,
	status string,
) db.Deployment {
	d, err := repo.Queries.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID:     releaseID,
			EnvironmentID: envID,
			Status:        status,
			StartedAt:     sql.NullInt64{},
			FinishedAt:    sql.NullInt64{},
			Forced:        0,
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	return d
}

func withAPIUser(r *http.Request, u *db.User) *http.Request {
	return auth.SetUser(r, u)
}

func withAPIURLParam(r *http.Request, key, value string) *http.Request {
	rctx, _ := r.Context().Value(chi.RouteCtxKey).(*chi.Context)
	if rctx == nil {
		rctx = chi.NewRouteContext()
	}
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// withAPIProjectID injects the project access key that
// RequireProjectAccess middleware would normally set. Tests that call
// the handler directly (bypassing the router) need this so the R2
// ownership check has a project id to compare against.
func withAPIProjectID(r *http.Request, projectID int64) *http.Request {
	return r.WithContext(
		context.WithValue(r.Context(), auth.ProjectAccessKey{}, projectID),
	)
}

func mustDecode(t *testing.T, body io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func mustParseCron(t *testing.T, parser cron.Parser, expr string) time.Time {
	t.Helper()
	sched, err := parser.Parse(expr)
	if err != nil {
		t.Fatalf("parse cron: %v", err)
	}
	next := sched.Next(time.Now())
	if next.IsZero() {
		t.Fatal("cron expression never fires")
	}
	return next
}

func TestListReleases(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	seedRelease(t, h.repo, p.ID)

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/releases", p.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	rec := httptest.NewRecorder()

	api.NewReleaseHandler(h.repo).ListReleases(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeListBody(t, rec.Body.Bytes())
	if len(resp) != 1 {
		t.Fatalf("expected 1 release, got %d", len(resp))
	}
}

func TestCreateRelease(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	if _, err := h.repo.Queries.CreateStep(
		context.Background(),
		db.CreateStepParams{
			ProjectID:  p.ID,
			Name:       "step",
			ScriptBody: "echo hi",
			SortOrder:  1,
		},
	); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"version":"2.0.0"}`)
	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/releases", p.ID),
		body,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	rec := httptest.NewRecorder()

	api.NewReleaseHandler(h.repo).CreateRelease(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["version"] != "2.0.0" {
		t.Fatalf("expected version 2.0.0, got %v", resp["version"])
	}
}

func TestCreateRelease_MissingVersion(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)

	body := strings.NewReader(`{"version":""}`)
	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/releases", p.ID),
		body,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	rec := httptest.NewRecorder()

	api.NewReleaseHandler(h.repo).CreateRelease(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestGetRelease(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/releases/%d", p.ID, r.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "relId", fmt.Sprint(r.ID))
	rec := httptest.NewRecorder()

	api.NewReleaseHandler(h.repo).GetRelease(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["id"] != float64(r.ID) {
		t.Fatalf("expected release id %d, got %v", r.ID, resp["id"])
	}
}

func TestGetRelease_NotFound(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/releases/999", p.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "relId", "999")
	rec := httptest.NewRecorder()

	api.NewReleaseHandler(h.repo).GetRelease(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRefreshRelease(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	if _, err := h.repo.Queries.CreateStep(
		context.Background(),
		db.CreateStepParams{
			ProjectID:  p.ID,
			Name:       "new-step",
			ScriptBody: "echo refreshed",
			SortOrder:  1,
		},
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/releases/%d/refresh", p.ID, r.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "relId", fmt.Sprint(r.ID))
	rec := httptest.NewRecorder()

	api.NewReleaseHandler(h.repo).RefreshRelease(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if !strings.Contains(resp["steps_json"].(string), "new-step") {
		t.Fatalf("expected refreshed steps, got %s", resp["steps_json"])
	}
}

func TestListDeployments(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	seedDeployment(t, h.repo, r.ID, e.ID, "pending")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/deployments", nil)
	req = withAPIUser(req, u)
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).ListDeployments(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["total"] != float64(1) {
		t.Fatalf("expected total 1, got %v", resp["total"])
	}
}

func TestListDeployments_ByProject_RouteRegistered(t *testing.T) {
	// ponytail: regression for "handler exists but route missing" — the
	// handler-level TestListDeployments above would pass even if the
	// /api/v1/projects/{id}/deployments GET route was never wired, so we
	// must hit it through the real router to catch that class of bug.
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	_, tokenPlain := seedAPIToken(t, h.repo, u.ID)
	p := seedProject(t, h.repo)
	otherP := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	otherR := seedRelease(t, h.repo, otherP.ID)
	seedDeployment(t, h.repo, r.ID, e.ID, "pending")
	seedDeployment(t, h.repo, r.ID, e.ID, "running")
	seedDeployment(
		t,
		h.repo,
		otherR.ID,
		e.ID,
		"pending",
	) // different project — must be filtered out

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	rtr := server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/deployments", p.ID),
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+tokenPlain)
	rec := httptest.NewRecorder()

	rtr.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if got := resp["total"]; got != float64(2) {
		t.Fatalf(
			"expected total 2 for project %d, got %v (body: %s)",
			p.ID,
			got,
			rec.Body.String(),
		)
	}
}

func TestGetDeployment(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "pending")

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/deployments/%d", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).GetDeployment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["id"] != float64(d.ID) {
		t.Fatalf("expected deployment id %d, got %v", d.ID, resp["id"])
	}
}

func TestGetDeploymentStatus(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "running")

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/deployments/%d/status", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).GetDeploymentStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	mustDecode(t, rec.Body, &resp)
	if resp["status"] != "running" {
		t.Fatalf("expected running, got %v", resp["status"])
	}
}

func TestCancelDeployment_Success(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "running")

	cancelled := false
	h.runner.RegisterCancel(d.ID, func() { cancelled = true })

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/cancel", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).CancelDeployment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !cancelled {
		t.Fatal("expected cancel func to be called")
	}
	var resp map[string]string
	mustDecode(t, rec.Body, &resp)
	if resp["status"] != "cancelled" {
		t.Fatalf("expected cancelled, got %v", resp["status"])
	}
}

func TestCancelDeployment_NotRunning(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "pending")

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/cancel", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).CancelDeployment(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestApproveDeployment_AdminOnly(t *testing.T) {
	h := newAPIHarness(t)
	admin := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "pending_approval")

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/approve", d.ID),
		nil,
	)
	req = withAPIUser(req, admin)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).ApproveDeployment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	mustDecode(t, rec.Body, &resp)
	if resp["status"] != "approved" {
		t.Fatalf("expected approved, got %v", resp["status"])
	}
}

func TestApproveDeployment_ViewerForbidden(t *testing.T) {
	h := newAPIHarness(t)
	viewer := seedAPIUser(t, h.repo, "viewer@example.com", "viewer")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "pending_approval")

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/approve", d.ID),
		nil,
	)
	req = withAPIUser(req, viewer)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).ApproveDeployment(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestRedeployDeployment(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "succeeded")

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/redeploy", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).RedeployDeployment(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["release_id"] != float64(r.ID) {
		t.Fatalf("expected release_id %d, got %v", r.ID, resp["release_id"])
	}
	if resp["environment_id"] != float64(e.ID) {
		t.Fatalf(
			"expected environment_id %d, got %v",
			e.ID,
			resp["environment_id"],
		)
	}
}

func TestRedeployDeployment_NotTerminal(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "running")

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/redeploy", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewDeploymentHandler(h.repo, h.runner).RedeployDeployment(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestListSchedules(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	next := mustParseCron(t, parser, "0 9 * * *")
	if _, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     p.ID,
			ReleaseID:     r.ID,
			EnvironmentID: e.ID,
			Cron:          "0 9 * * *",
			NextRunAt:     next.Unix(),
			Enabled:       1,
			LastFiredAt:   sql.NullInt64{},
			Note:          sql.NullString{},
		},
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/schedules", p.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	rec := httptest.NewRecorder()

	api.NewScheduledDeploymentHandler(h.repo, parser).ListSchedules(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeListBody(t, rec.Body.Bytes())
	if len(resp) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(resp))
	}
}

func TestCreateSchedule(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	body := strings.NewReader(
		fmt.Sprintf(
			`{"release_id":%d,"environment_id":%d,"cron":"0 9 * * *","enabled":true,"note":"morning"}`,
			r.ID,
			e.ID,
		),
	)
	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/schedules", p.ID),
		body,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	rec := httptest.NewRecorder()

	api.NewScheduledDeploymentHandler(h.repo, parser).CreateSchedule(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["cron"] != "0 9 * * *" {
		t.Fatalf("expected cron, got %v", resp["cron"])
	}
}

func TestCreateSchedule_InvalidCron(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	body := strings.NewReader(
		fmt.Sprintf(
			`{"release_id":%d,"environment_id":%d,"cron":"not-a-cron"}`,
			r.ID,
			e.ID,
		),
	)
	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/schedules", p.ID),
		body,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	rec := httptest.NewRecorder()

	api.NewScheduledDeploymentHandler(h.repo, parser).CreateSchedule(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetSchedule(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	next := mustParseCron(t, parser, "0 9 * * *")
	s, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     p.ID,
			ReleaseID:     r.ID,
			EnvironmentID: e.ID,
			Cron:          "0 9 * * *",
			NextRunAt:     next.Unix(),
			Enabled:       1,
			LastFiredAt:   sql.NullInt64{},
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/schedules/%d", p.ID, s.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "schedId", fmt.Sprint(s.ID))
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()

	api.NewScheduledDeploymentHandler(h.repo, parser).GetSchedule(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateSchedule(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	next := mustParseCron(t, parser, "0 9 * * *")
	s, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     p.ID,
			ReleaseID:     r.ID,
			EnvironmentID: e.ID,
			Cron:          "0 9 * * *",
			NextRunAt:     next.Unix(),
			Enabled:       1,
			LastFiredAt:   sql.NullInt64{},
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(
		fmt.Sprintf(
			`{"release_id":%d,"environment_id":%d,"cron":"0 10 * * *","enabled":false,"note":"updated"}`,
			r.ID,
			e.ID,
		),
	)
	req := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/v1/projects/%d/schedules/%d", p.ID, s.ID),
		body,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "schedId", fmt.Sprint(s.ID))
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()

	api.NewScheduledDeploymentHandler(h.repo, parser).UpdateSchedule(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["cron"] != "0 10 * * *" {
		t.Fatalf("expected updated cron, got %v", resp["cron"])
	}
	if resp["enabled"] != float64(0) {
		t.Fatalf("expected disabled, got %v", resp["enabled"])
	}
}

func TestDeleteSchedule(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	next := mustParseCron(t, parser, "0 9 * * *")
	s, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     p.ID,
			ReleaseID:     r.ID,
			EnvironmentID: e.ID,
			Cron:          "0 9 * * *",
			NextRunAt:     next.Unix(),
			Enabled:       1,
			LastFiredAt:   sql.NullInt64{},
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/v1/projects/%d/schedules/%d", p.ID, s.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "schedId", fmt.Sprint(s.ID))
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()

	api.NewScheduledDeploymentHandler(h.repo, parser).DeleteSchedule(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestToggleSchedule(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	next := mustParseCron(t, parser, "0 9 * * *")
	s, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     p.ID,
			ReleaseID:     r.ID,
			EnvironmentID: e.ID,
			Cron:          "0 9 * * *",
			NextRunAt:     next.Unix(),
			Enabled:       1,
			LastFiredAt:   sql.NullInt64{},
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/schedules/%d/toggle", p.ID, s.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "schedId", fmt.Sprint(s.ID))
	req = withAPIProjectID(req, p.ID)
	rec := httptest.NewRecorder()

	api.NewScheduledDeploymentHandler(h.repo, parser).ToggleSchedule(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	if resp["enabled"] != float64(0) {
		t.Fatalf("expected disabled after toggle, got %v", resp["enabled"])
	}
}

func TestStreamLogs_SSE(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "running")

	if _, err := h.repo.Queries.CreateDeploymentLog(
		context.Background(),
		db.CreateDeploymentLogParams{
			DeploymentID: d.ID,
			StepName:     sql.NullString{String: "step1", Valid: true},
			Line:         "historical log",
		},
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		100*time.Millisecond,
	)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/deployments/%d/logs/stream", d.ID), nil).
		WithContext(ctx)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		api.NewLogHandler(h.broker, h.repo).StreamLogs(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after context timeout")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: historical log") {
		t.Fatalf("expected historical log in body, got %s", body)
	}
}

func TestStreamLogs_NDJSON(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "running")

	if _, err := h.repo.Queries.CreateDeploymentLog(
		context.Background(),
		db.CreateDeploymentLogParams{
			DeploymentID: d.ID,
			StepName:     sql.NullString{String: "step1", Valid: true},
			Line:         "historical log",
		},
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		100*time.Millisecond,
	)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/deployments/%d/logs/stream?format=ndjson", d.ID), nil).
		WithContext(ctx)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		api.NewLogHandler(h.broker, h.repo).StreamLogs(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after context timeout")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("expected application/x-ndjson, got %s", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"line":"historical log"`) {
		t.Fatalf("expected ndjson line in body, got %s", body)
	}
	if !strings.Contains(body, `"step":"step1"`) {
		t.Fatalf("expected step in body, got %s", body)
	}
}

func TestStreamLogs_BadFormat(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "running")

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/deployments/%d/logs/stream?format=xml", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewLogHandler(h.broker, h.repo).StreamLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestExportLogs(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, r.ID, e.ID, "succeeded")

	if _, err := h.repo.Queries.CreateDeploymentLog(
		context.Background(),
		db.CreateDeploymentLogParams{
			DeploymentID: d.ID,
			StepName:     sql.NullString{String: "build", Valid: true},
			Line:         "build log",
		},
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/deployments/%d/logs.txt", d.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(d.ID))
	rec := httptest.NewRecorder()

	api.NewLogHandler(h.broker, h.repo).ExportLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "build log") {
		t.Fatalf("expected build log in body, got %s", body)
	}
	if !strings.Contains(body, "api-test") {
		t.Fatalf("expected project name in body, got %s", body)
	}
}

func TestExportLogs_NotFound(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/deployments/999/logs.txt",
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", "999")
	rec := httptest.NewRecorder()

	api.NewLogHandler(h.broker, h.repo).ExportLogs(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func seedAPIToken(
	t *testing.T,
	repo *repository.Repository,
	userID int64,
) (id, plaintext string) {
	t.Helper()
	full, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint api token: %v", err)
	}
	id = uuid.NewString()
	if _, err := repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID:          id,
			UserID:      userID,
			Name:        "audit-test-token",
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
			ExpiresAt:   sql.NullInt64{},
		},
	); err != nil {
		t.Fatalf("create api token: %v", err)
	}
	return id, full
}

func TestAPIWriteCreatesAuditLogWithoutTokenLeak(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	_, tokenPlain := seedAPIToken(t, h.repo, u.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	r := server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)

	body := strings.NewReader(`{"name":"audit-env"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/environments", body)
	req.Header.Set("Authorization", "Bearer "+tokenPlain)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	logs, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			FAction: sql.NullString{
				String: "create_environment",
				Valid:  true,
			},
			PageLimit: 10,
		},
	)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf(
			"expected 1 audit log for create_environment, got %d",
			len(logs),
		)
	}
	entry := logs[0]
	if entry.UserID.Int64 != u.ID {
		t.Fatalf("expected audit user_id %d, got %d", u.ID, entry.UserID.Int64)
	}
	if !strings.Contains(entry.Action, "create_environment") {
		t.Fatalf("expected action create_environment, got %s", entry.Action)
	}
	if strings.Contains(entry.Details.String, tokenPlain) {
		t.Fatalf("audit details must not contain bearer token plaintext")
	}
}

// TestR1_NonAdminCannotManageUsers guards the R1 ship-blocker:
// /api/v1/admin/users/* (except /users/me) must reject non-admin bearer
// tokens. Before the fix, a deployer could create, modify, or delete
// users — including promoting themselves to admin.
func TestR1_NonAdminCannotManageUsers(t *testing.T) {
	h := newAPIHarness(t)
	deployer := seedAPIUser(t, h.repo, "deployer@example.com", "deployer")
	_, tokenPlain := seedAPIToken(t, h.repo, deployer.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	r := server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list", http.MethodGet, "/api/v1/admin/users", ""},
		{"get", http.MethodGet, "/api/v1/admin/users/1", ""},
		{
			"create",
			http.MethodPost,
			"/api/v1/admin/users",
			`{"email":"x@x","name":"x","role":"admin","password":"pwpwpwpwpwpwpwpw"}`,
		},
		{
			"update",
			http.MethodPut,
			"/api/v1/admin/users/1",
			`{"name":"x","role":"admin"}`,
		},
		{"delete", http.MethodDelete, "/api/v1/admin/users/1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Header.Set("Authorization", "Bearer "+tokenPlain)
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf(
					"expected 403 for %s %s, got %d: %s",
					tc.method,
					tc.path,
					rec.Code,
					rec.Body.String(),
				)
			}
		})
	}

	// Sanity: /users/me must STILL work for a non-admin.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenPlain)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected 200 for /users/me as deployer, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}
}

// TestR1_AdminCanStillManageUsers confirms the R1 fix didn't break
// the admin happy path. An admin token must still be able to list
// users.
func TestR1_AdminCanStillManageUsers(t *testing.T) {
	h := newAPIHarness(t)
	admin := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	_, tokenPlain := seedAPIToken(t, h.repo, admin.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	r := server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenPlain)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected 200 for admin GET /admin/users, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}
}

// TestR1_AdminCannotDeleteSelf guards the R11 ship-blocker: even with
// the admin role, the bearer token's own user must not be deletable.
func TestR1_AdminCannotDeleteSelf(t *testing.T) {
	h := newAPIHarness(t)
	admin := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	_, tokenPlain := seedAPIToken(t, h.repo, admin.ID)

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	r := server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)

	req := httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/v1/admin/users/%d", admin.ID),
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+tokenPlain)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf(
			"expected 403 for self-delete, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}
}

// TestR2_VariableCrossProjectIDOR guards the R2 ship-blocker: a
// member of project A must not be able to read, update, or delete
// variables that belong to project B by guessing variable IDs. The
// decrypted secret value would be returned on a successful GET.
func TestR2_VariableCrossProjectIDOR(t *testing.T) {
	h := newAPIHarness(t)
	ownerOfA := seedAPIUser(t, h.repo, "ownerA@example.com", "deployer")
	ownerOfB := seedAPIUser(t, h.repo, "ownerB@example.com", "deployer")
	_, tokenPlain := seedAPIToken(t, h.repo, ownerOfA.ID)

	projectA := seedProject(t, h.repo)
	projectB := seedProject(t, h.repo)
	// OwnerA is a member of A only.
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projectA.ID,
			UserID:    ownerOfA.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	// OwnerB is a member of B (so B's variable row exists for a real
	// owner; the test is about A's bearer token reaching across).
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projectB.ID,
			UserID:    ownerOfB.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}

	// B's secret variable — the row ownerA must NOT be able to read.
	bVar, err := h.repo.CreateVariable(
		context.Background(),
		db.CreateVariableParams{
			ProjectID: projectB.ID,
			Name:      "B_SECRET",
			Value: sql.NullString{
				String: "very-secret-B-value",
				Valid:  true,
			},
			EnvironmentID: sql.NullInt64{},
			Secret:        1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	r := server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)

	t.Run("GET cross-project variable returns 404", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodGet,
			fmt.Sprintf(
				"/api/v1/projects/%d/variables/%d",
				projectA.ID,
				bVar.ID,
			),
			nil,
		)
		req.Header.Set("Authorization", "Bearer "+tokenPlain)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
		// The leaked-secret smoking gun: the response body must NOT
		// contain the secret value.
		if strings.Contains(rec.Body.String(), "very-secret-B-value") {
			t.Fatalf(
				"response leaked project-B secret value: %s",
				rec.Body.String(),
			)
		}
	})

	t.Run("PUT cross-project variable returns 404", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPut,
			fmt.Sprintf(
				"/api/v1/projects/%d/variables/%d",
				projectA.ID,
				bVar.ID,
			),
			strings.NewReader(`{"name":"pwned","value":"pwned"}`),
		)
		req.Header.Set("Authorization", "Bearer "+tokenPlain)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("DELETE cross-project variable returns 404", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodDelete,
			fmt.Sprintf(
				"/api/v1/projects/%d/variables/%d",
				projectA.ID,
				bVar.ID,
			),
			nil,
		)
		req.Header.Set("Authorization", "Bearer "+tokenPlain)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
		// Verify the row still exists in B.
		got, err := h.repo.Queries.GetVariable(context.Background(), bVar.ID)
		if err != nil {
			t.Fatalf("project-B variable was deleted: %v", err)
		}
		if got.ID != bVar.ID {
			t.Fatalf("expected variable %d to survive, got %d", bVar.ID, got.ID)
		}
	})

	// Sanity: ownerA can still see their OWN project's variables.
	aVar, err := h.repo.CreateVariable(
		context.Background(),
		db.CreateVariableParams{
			ProjectID:     projectA.ID,
			Name:          "A_OK",
			Value:         sql.NullString{String: "a-value", Valid: true},
			EnvironmentID: sql.NullInt64{},
			Secret:        0,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("GET own-project variable still works", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodGet,
			fmt.Sprintf(
				"/api/v1/projects/%d/variables/%d",
				projectA.ID,
				aVar.ID,
			),
			nil,
		)
		req.Header.Set("Authorization", "Bearer "+tokenPlain)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf(
				"expected 200 for own-project variable, got %d: %s",
				rec.Code,
				rec.Body.String(),
			)
		}
	})
}

// TestR2_StepCrossProjectIDOR is the same shape as the variable IDOR
// test, but for steps. Steps carry bash scripts; cross-project
// read/update would leak code and let an attacker rewrite project
// B's deployment logic.
func TestR2_StepCrossProjectIDOR(t *testing.T) {
	h := newAPIHarness(t)
	ownerOfA := seedAPIUser(t, h.repo, "ownerA@example.com", "deployer")
	_, tokenPlain := seedAPIToken(t, h.repo, ownerOfA.ID)

	projectA := seedProject(t, h.repo)
	projectB := seedProject(t, h.repo)
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projectA.ID,
			UserID:    ownerOfA.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}

	bStep, err := h.repo.Queries.CreateStep(
		context.Background(),
		db.CreateStepParams{
			ProjectID:  projectB.ID,
			Name:       "B_SECRET_STEP",
			ScriptBody: "echo B-SECRET-COMMAND",
			SortOrder:  1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	r := server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)

	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(m+" cross-project step returns 404", func(t *testing.T) {
			var body io.Reader
			if m == http.MethodPut {
				body = strings.NewReader(
					`{"name":"pwned","script_body":"pwned","sort_order":1}`,
				)
			}
			req := httptest.NewRequest(
				m,
				fmt.Sprintf(
					"/api/v1/projects/%d/steps/%d",
					projectA.ID,
					bStep.ID,
				),
				body,
			)
			req.Header.Set("Authorization", "Bearer "+tokenPlain)
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf(
					"%s: expected 404, got %d: %s",
					m,
					rec.Code,
					rec.Body.String(),
				)
			}
			if strings.Contains(rec.Body.String(), "B-SECRET-COMMAND") {
				t.Fatalf(
					"response leaked project-B step body: %s",
					rec.Body.String(),
				)
			}
		})
	}
}
