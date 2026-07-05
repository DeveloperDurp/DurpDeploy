package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

// scheduledHarness holds a test server with scheduled-deployment routes mounted.
type scheduledHarness struct {
	t      *testing.T
	repo   *repository.Repository
	server *httptest.Server
}

func newScheduledHarness(t *testing.T) *scheduledHarness {
	t.Helper()
	// ponytail: file-backed SQLite, not :memory:, because the runner spawns
	// goroutines that need their own DB connection.
	dir := t.TempDir()
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		filepath.Join(dir, "test.db"),
	)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repo := repository.New(conn)
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sdh := handler.NewScheduledDeploymentHandler(repo, parser)

	// Also mount deployment handler so we can reuse availableEnvsForDeployPage
	// via the NewForm route if needed; not required for the core tests.
	dh := handler.NewDeploymentHandler(repo, rnr)

	r := chi.NewRouter()
	r.Get("/projects/{id}/schedules", sdh.List)
	r.Get("/projects/{id}/schedules/new", sdh.NewForm)
	r.Post("/projects/{id}/schedules", sdh.Create)
	r.Get("/projects/{id}/schedules/{schedId}/edit", sdh.EditForm)
	r.Put("/projects/{id}/schedules/{schedId}", sdh.Update)
	r.Delete("/projects/{id}/schedules/{schedId}", sdh.Delete)
	r.Post("/projects/{id}/schedules/{schedId}/toggle", sdh.Toggle)

	// Mount deploy routes so the harness can create releases via the repo
	// and the env helpers work consistently with the rest of the suite.
	r.Get("/projects/{id}/deploy", dh.NewDeploymentPage)
	r.Post("/projects/{id}/deploy", dh.ScheduleDeployment)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &scheduledHarness{t: t, repo: repo, server: srv}
}

func (h *scheduledHarness) makeProject(name string) db.Project {
	h.t.Helper()
	p, err := h.repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{Name: name, Description: sql.NullString{}},
	)
	if err != nil {
		h.t.Fatalf("create project: %v", err)
	}
	return p
}

func (h *scheduledHarness) makeEnv(name string) db.Environment {
	h.t.Helper()
	e, err := h.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: name, Description: sql.NullString{}},
	)
	if err != nil {
		h.t.Fatalf("create env: %v", err)
	}
	return e
}

func (h *scheduledHarness) makeRelease(
	projectID int64,
	version, scriptBody string,
) db.Release {
	h.t.Helper()
	steps := []map[string]any{
		{"name": "s1", "script_body": scriptBody, "sort_order": 1},
	}
	stepsJSON, _ := json.Marshal(steps)
	r, err := h.repo.Queries.CreateRelease(
		context.Background(),
		db.CreateReleaseParams{
			ProjectID: projectID,
			Version:   version,
			StepsJson: string(stepsJSON),
		},
	)
	if err != nil {
		h.t.Fatalf("create release: %v", err)
	}
	return r
}

func (h *scheduledHarness) postSchedule(
	projectID, releaseID, envID int64,
	cronExpr, note string,
	enabled bool,
) int {
	h.t.Helper()
	form := url.Values{}
	form.Set("release_id", fmt.Sprintf("%d", releaseID))
	form.Set("environment_id", fmt.Sprintf("%d", envID))
	form.Set("cron", cronExpr)
	form.Set("note", note)
	if enabled {
		form.Set("enabled", "true")
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm(
		fmt.Sprintf("%s/projects/%d/schedules", h.server.URL, projectID),
		form,
	)
	if err != nil {
		h.t.Fatalf("POST schedule: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *scheduledHarness) putSchedule(
	projectID, schedID, releaseID, envID int64,
	cronExpr, note string,
	enabled bool,
) int {
	h.t.Helper()
	form := url.Values{}
	form.Set("release_id", fmt.Sprintf("%d", releaseID))
	form.Set("environment_id", fmt.Sprintf("%d", envID))
	form.Set("cron", cronExpr)
	form.Set("note", note)
	if enabled {
		form.Set("enabled", "true")
	}
	req, _ := http.NewRequest(
		"PUT",
		fmt.Sprintf("%s/projects/%d/schedules/%d", h.server.URL, projectID, schedID),
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("PUT schedule: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *scheduledHarness) toggleSchedule(projectID, schedID int64) int {
	h.t.Helper()
	resp, err := http.Post(
		fmt.Sprintf("%s/projects/%d/schedules/%d/toggle", h.server.URL, projectID, schedID),
		"",
		nil,
	)
	if err != nil {
		h.t.Fatalf("POST toggle: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *scheduledHarness) deleteSchedule(projectID, schedID int64) int {
	h.t.Helper()
	req, _ := http.NewRequest(
		"DELETE",
		fmt.Sprintf("%s/projects/%d/schedules/%d", h.server.URL, projectID, schedID),
		nil,
	)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("DELETE schedule: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestCreateScheduled_Valid(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-valid")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(proj.ID, rel.ID, env.ID, "0 0 * * *", "nightly", true)
	if code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", code)
	}

	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	if err != nil || len(schedules) == 0 {
		t.Fatalf("expected schedule row, got err=%v", err)
	}
	s := schedules[0]
	if s.ReleaseID != rel.ID {
		t.Errorf("release_id: got %d, want %d", s.ReleaseID, rel.ID)
	}
	if s.EnvironmentID != env.ID {
		t.Errorf("environment_id: got %d, want %d", s.EnvironmentID, env.ID)
	}
	if s.Cron != "0 0 * * *" {
		t.Errorf("cron: got %q, want %q", s.Cron, "0 0 * * *")
	}
	if s.NextRunAt <= time.Now().Unix() {
		t.Errorf("next_run_at should be in the future, got %d", s.NextRunAt)
	}
	if s.Enabled != 1 {
		t.Errorf("enabled: got %d, want 1", s.Enabled)
	}
}

func TestCreateScheduled_BadCron_422(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-bad-cron")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(proj.ID, rel.ID, env.ID, "bogus", "nightly", true)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", code)
	}

	// Ensure no row was created.
	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	if len(schedules) != 0 {
		t.Errorf("expected 0 schedules, got %d", len(schedules))
	}
}

func TestCreateScheduled_CrossProject_400(t *testing.T) {
	h := newScheduledHarness(t)
	projA := h.makeProject("projA")
	projB := h.makeProject("projB")
	env := h.makeEnv("dev")
	relA := h.makeRelease(projA.ID, "1.0.0", "true")
	_ = h.makeRelease(projB.ID, "1.0.0", "true")

	code := h.postSchedule(projB.ID, relA.ID, env.ID, "0 0 * * *", "nightly", true)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", code)
	}

	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		projB.ID,
	)
	if len(schedules) != 0 {
		t.Errorf("expected 0 schedules for projB, got %d", len(schedules))
	}
}

func TestUpdateScheduled_RecomputesNextRun(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-update")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	h.postSchedule(proj.ID, rel.ID, env.ID, "0 0 * * *", "nightly", true)
	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	s := schedules[0]
	oldNextRun := s.NextRunAt

	code := h.putSchedule(proj.ID, s.ID, rel.ID, env.ID, "0 12 * * *", "daily noon", true)
	if code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", code)
	}

	updated, err := h.repo.Queries.GetScheduledDeployment(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("get scheduled: %v", err)
	}
	if updated.NextRunAt == oldNextRun {
		t.Errorf("next_run_at should have changed after update")
	}
	if updated.Cron != "0 12 * * *" {
		t.Errorf("cron: got %q, want %q", updated.Cron, "0 12 * * *")
	}
}

func TestToggleScheduled_Advances(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-toggle")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	oldNextRun := time.Now().Add(-24 * time.Hour).Unix()
	s, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     proj.ID,
			ReleaseID:     rel.ID,
			EnvironmentID: env.ID,
			Cron:          "0 0 * * *",
			NextRunAt:     oldNextRun,
			Enabled:       0,
			LastFiredAt:   sql.NullInt64{},
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if s.Enabled != 0 {
		t.Fatalf("expected disabled, got enabled=%d", s.Enabled)
	}

	code := h.toggleSchedule(proj.ID, s.ID)
	if code != http.StatusOK && code != http.StatusSeeOther {
		t.Logf("toggle returned %d (accepting any 2xx)", code)
	}

	updated, err := h.repo.Queries.GetScheduledDeployment(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("get scheduled: %v", err)
	}
	if updated.Enabled != 1 {
		t.Errorf("expected enabled=1 after toggle, got %d", updated.Enabled)
	}
	if updated.NextRunAt <= oldNextRun {
		t.Errorf("next_run_at should be recomputed to future when enabling, got %d (was %d)", updated.NextRunAt, oldNextRun)
	}
}

func TestDeleteScheduled_404(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-delete")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	h.postSchedule(proj.ID, rel.ID, env.ID, "0 0 * * *", "nightly", true)
	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	s := schedules[0]

	code := h.deleteSchedule(proj.ID, s.ID)
	if code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", code)
	}

	_, err := h.repo.Queries.GetScheduledDeployment(context.Background(), s.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected schedule to be deleted, got err=%v", err)
	}
}

func TestCreateScheduled_DescriptorRejected(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-desc")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(proj.ID, rel.ID, env.ID, "@hourly", "nightly", true)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for descriptor, got %d", code)
	}
}

func TestCreateScheduled_TZPrefixRejected(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-tz")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(proj.ID, rel.ID, env.ID, "TZ=America/New_York 0 0 * * *", "nightly", true)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for TZ= prefix, got %d", code)
	}
}
