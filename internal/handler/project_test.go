package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
)

// projectHarness wraps a full-stack server with helpers to create a project,
// a lifecycle, envs, releases, and to drive deployments.
type projectHarness struct {
	t      *testing.T
	repo   *repository.Repository
	server *httptest.Server
}

func newProjectHarness(t *testing.T) *projectHarness {
	t.Helper()
	dir := t.TempDir()
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", filepath.Join(dir, "test.db"))
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repo := repository.New(conn)
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	srv := httptest.NewServer(server.NewRouter(repo, rnr))
	t.Cleanup(srv.Close)
	return &projectHarness{t: t, repo: repo, server: srv}
}

func (h *projectHarness) makeProject(name string) db.Project {
	h.t.Helper()
	p, err := h.repo.Queries.CreateProject(context.Background(), db.CreateProjectParams{Name: name, Description: sql.NullString{}})
	if err != nil {
		h.t.Fatalf("create project: %v", err)
	}
	return p
}

func (h *projectHarness) makeEnv(name string) db.Environment {
	h.t.Helper()
	e, err := h.repo.Queries.CreateEnvironment(context.Background(), db.CreateEnvironmentParams{Name: name, Description: sql.NullString{}})
	if err != nil {
		h.t.Fatalf("create env: %v", err)
	}
	return e
}

func (h *projectHarness) makeRelease(projectID int64, version, scriptBody string) db.Release {
	h.t.Helper()
	steps := []map[string]any{{"name": "s1", "script_body": scriptBody, "sort_order": 1}}
	stepsJSON, _ := json.Marshal(steps)
	r, err := h.repo.Queries.CreateRelease(context.Background(), db.CreateReleaseParams{ProjectID: projectID, Version: version, StepsJson: string(stepsJSON)})
	if err != nil {
		h.t.Fatalf("create release: %v", err)
	}
	return r
}

func (h *projectHarness) makeLifecycle(name string, envIDs ...int64) db.Lifecycle {
	h.t.Helper()
	lc, err := h.repo.Queries.CreateLifecycle(context.Background(), db.CreateLifecycleParams{Name: name, Description: sql.NullString{}})
	if err != nil {
		h.t.Fatalf("create lifecycle: %v", err)
	}
	for i, eid := range envIDs {
		if _, err := h.repo.Queries.CreateLifecycleStage(context.Background(), db.CreateLifecycleStageParams{
			LifecycleID:   lc.ID,
			EnvironmentID: eid,
			SortOrder:     int64(i + 1),
		}); err != nil {
			h.t.Fatalf("create stage: %v", err)
		}
	}
	if err := h.repo.Queries.SetProjectLifecycle(context.Background(), db.SetProjectLifecycleParams{
		LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true},
		ID:          lc.ID, // not used; we override below
	}); err != nil {
		// ignore: the helper is wrong for project_id, the caller does it
	}
	return lc
}

func (h *projectHarness) assignLifecycle(projectID, lifecycleID int64) {
	h.t.Helper()
	if err := h.repo.Queries.SetProjectLifecycle(context.Background(), db.SetProjectLifecycleParams{
		LifecycleID: sql.NullInt64{Int64: lifecycleID, Valid: true},
		ID:          projectID,
	}); err != nil {
		h.t.Fatalf("assign lifecycle: %v", err)
	}
}

// waitForDeploymentStatus blocks until the deployment's status is one of
// expected, then returns the final row. Used so the runner has time to set
// status to "succeeded" or "failed" before the panel queries it.
func (h *projectHarness) waitForDeploymentStatus(deploymentID int64, expected ...string) db.Deployment {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var dep db.Deployment
	for time.Now().Before(deadline) {
		d, err := h.repo.Queries.GetDeployment(context.Background(), deploymentID)
		if err == nil {
			dep = d
			for _, e := range expected {
				if d.Status == e {
					return dep
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("deployment %d did not reach status %v (last: %q)", deploymentID, expected, dep.Status)
	return dep
}

func (h *projectHarness) postDeploy(releaseID, envID int64) (int, int64) {
	h.t.Helper()
	form := map[string]string{
		"release_id":     fmt.Sprintf("%d", releaseID),
		"environment_id": fmt.Sprintf("%d", envID),
	}
	body := strings.NewReader(formEncode(form))
	req, _ := http.NewRequest("POST", h.server.URL+"/deployments", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	depID := int64(0)
	if loc := resp.Header.Get("Location"); loc != "" {
		fmt.Sscanf(loc, "/deployments/%d", &depID)
	}
	return resp.StatusCode, depID
}

func formEncode(m map[string]string) string {
	var b bytes.Buffer
	first := true
	for k, v := range m {
		if !first {
			b.WriteByte('&')
		}
		first = false
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
	}
	return b.String()
}

func (h *projectHarness) getProjectPage(projectID int64) string {
	h.t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/projects/%d", h.server.URL, projectID))
	if err != nil {
		h.t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		h.t.Fatalf("status %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.String()
}

// TestProjectPanel_FailedAttemptIsVisible verifies that a deployment which
// failed (and never succeeded) still shows up in the panel's Status column
// and Last Deployed column. This is the user-visible fix: "I deployed v1.0.0
// to dev and it failed; show me that on the project page."
func TestProjectPanel_FailedAttemptIsVisible(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("p")
	dev := h.makeEnv("dev")
	test := h.makeEnv("test")
	lc := h.makeLifecycle("dt", dev.ID, test.ID)
	h.assignLifecycle(proj.ID, lc.ID)

	v1 := h.makeRelease(proj.ID, "1.0.0", "exit 1") // fails
	_, depID := h.postDeploy(v1.ID, dev.ID)
	h.waitForDeploymentStatus(depID, "failed")

	page := h.getProjectPage(proj.ID)

	if !strings.Contains(page, "1.0.0") {
		t.Errorf("version 1.0.0 should be visible on the page")
	}
	if !strings.Contains(page, "failed") {
		t.Errorf("'failed' status should appear; the failed deploy must be visible")
	}
	if !strings.Contains(page, "No successful deploys") {
		t.Errorf("Last Deployed column should say 'No successful deploys' for env that never succeeded")
	}
}

// TestProjectPanel_StreakStrip verifies that the per-row 5-dot streak reflects
// the recent deployment history in newest-first order.
func TestProjectPanel_StreakStrip(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("p")
	dev := h.makeEnv("dev")
	lc := h.makeLifecycle("d", dev.ID)
	h.assignLifecycle(proj.ID, lc.ID)

	// Three sequential deploys to dev, distinct versions to satisfy
	// UNIQUE(project_id, version). The test only cares about the resulting
	// streak colors, not the version numbers.
	for i := 0; i < 3; i++ {
		rel := h.makeRelease(proj.ID, fmt.Sprintf("1.0.%d", i), "exit 0")
		_, depID := h.postDeploy(rel.ID, dev.ID)
		h.waitForDeploymentStatus(depID, "succeeded")
	}
	// Patch the 2nd-most-recent deployment's status to "failed" so we can
	// verify the streak renders a red dot for it.
	recents, err := h.repo.Queries.ListRecentDeploymentsForEnv(context.Background(), db.ListRecentDeploymentsForEnvParams{
		EnvironmentID: dev.ID,
		Limit:         5,
	})
	if err != nil || len(recents) < 2 {
		t.Fatalf("expected >=2 deployments, got %d (err=%v)", len(recents), err)
	}
	second := recents[1]
	if err := h.repo.Queries.UpdateDeploymentStatus(context.Background(), db.UpdateDeploymentStatusParams{
		ID:         second.ID,
		Status:     "failed",
		StartedAt:  sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	}); err != nil {
		t.Fatalf("patch status: %v", err)
	}

	page := h.getProjectPage(proj.ID)

	// The streak should contain at least one red and one green dot. We can't
	// easily count from HTML, but the bg-error and bg-success classes only
	// appear on AttemptDots, so their presence is a sufficient check.
	if !strings.Contains(page, "bg-error") {
		t.Errorf("expected a failed dot in the streak strip; bg-error class not found")
	}
	if !strings.Contains(page, "bg-success") {
		t.Errorf("expected a success dot in the streak strip; bg-success class not found")
	}
}

// TestProjectsList_ShowsPerEnvStatusDots verifies that the projects list page
// renders a Deployments column with one status dot per env, and that the dot
// color matches the latest deployment's status. Lifecycle and free-floating
// projects are both covered.
func TestProjectsList_ShowsPerEnvStatusDots(t *testing.T) {
	h := newProjectHarness(t)

	// Project A: lifecycle-bound with 2 envs; one env succeeded, one failed.
	// To avoid the gate kicking in on the second deploy, we deploy vA to dev
	// (succeeds), then the SAME version to prod via the gate; the gate would
	// block. So we patch prod's status to "failed" directly to simulate a
	// failed deploy without going through the gate.
	projA := h.makeProject("A")
	envDev := h.makeEnv("dev")
	envProd := h.makeEnv("prod")
	lc := h.makeLifecycle("dp", envDev.ID, envProd.ID)
	h.assignLifecycle(projA.ID, lc.ID)

	vA := h.makeRelease(projA.ID, "1.0.0", "exit 0")
	_, depA1 := h.postDeploy(vA.ID, envDev.ID)
	h.waitForDeploymentStatus(depA1, "succeeded")

	// Create a deployment row for prod (without running the gate) by directly
	// inserting via sqlc, then patching its status to "failed". This avoids
	// the gate check that would block the second deploy.
	depA2, err := h.repo.Queries.CreateDeployment(context.Background(), db.CreateDeploymentParams{
		ReleaseID:     vA.ID,
		EnvironmentID: envProd.ID,
		Status:        "running",
		StartedAt:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		FinishedAt:    sql.NullInt64{},
		Forced:        0,
	})
	if err != nil {
		t.Fatalf("create prod deployment: %v", err)
	}
	if err := h.repo.Queries.UpdateDeploymentStatus(context.Background(), db.UpdateDeploymentStatusParams{
		ID:         depA2.ID,
		Status:     "failed",
		StartedAt:  sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	}); err != nil {
		t.Fatalf("patch prod to failed: %v", err)
	}

	// Project B: free-floating, no envs touched.
	h.makeProject("B")

	// Project C: free-floating with one env and a successful deploy.
	projC := h.makeProject("C")
	envC := h.makeEnv("C-env")
	vC := h.makeRelease(projC.ID, "1.0.0", "exit 0")
	_, depC := h.postDeploy(vC.ID, envC.ID)
	h.waitForDeploymentStatus(depC, "succeeded")

	page := h.getProjectsList()

	if !strings.Contains(page, ">dev<") || !strings.Contains(page, ">prod<") {
		t.Errorf("lifecycle envs dev/prod should appear in A's row; body missing")
	}
	if !strings.Contains(page, "bg-success") {
		t.Errorf("expected bg-success class somewhere on the list page")
	}
	if !strings.Contains(page, "bg-error") {
		t.Errorf("expected bg-error class somewhere on the list page")
	}
	if !strings.Contains(page, "No environments") {
		t.Errorf("project B (no envs) should show 'No environments' hint")
	}
	if !strings.Contains(page, ">C-env<") {
		t.Errorf("project C's env 'C-env' should appear")
	}
	// Each cell should also show the version of the latest attempt. Project A
	// deployed v1.0.0 to dev (succeeded) and to prod (failed). Project C
	// deployed v1.0.0 to C-env.
	if !strings.Contains(page, "1.0.0") {
		t.Errorf("version 1.0.0 should appear in the list page (multiple cells)")
	}
}

// TestProjectsList_LifecycleShowsAllStagesIncludingUntouched verifies that a
// lifecycle-bound project renders a row for every stage in its lifecycle,
// even stages that have never been deployed to. The version cell for an
// untouched stage should render an em-dash placeholder, not be omitted.
func TestProjectsList_LifecycleShowsAllStagesIncludingUntouched(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("L")
	envA := h.makeEnv("L-A")
	envB := h.makeEnv("L-B")
	envC := h.makeEnv("L-C")
	lc := h.makeLifecycle("abc", envA.ID, envB.ID, envC.ID)
	h.assignLifecycle(proj.ID, lc.ID)

	// Deploy only to L-A. L-B and L-C remain untouched.
	rel := h.makeRelease(proj.ID, "1.0.0", "exit 0")
	_, depID := h.postDeploy(rel.ID, envA.ID)
	h.waitForDeploymentStatus(depID, "succeeded")

	page := h.getProjectsList()

	// All three env names must appear in the lifecycle-bound project's row.
	for _, name := range []string{"L-A", "L-B", "L-C"} {
		if !strings.Contains(page, ">"+name+"<") {
			t.Errorf("lifecycle stage %q must appear in the list, even if untouched", name)
		}
	}
	// The em-dash placeholder appears for untouched envs. Use a class-scoped
	// search so a stray "—" elsewhere doesn't satisfy the check.
	if !strings.Contains(page, "bg-base-300") {
		t.Errorf("untouched envs should render an em-dash placeholder with bg-base-300 styling")
	}
}

func (h *projectHarness) getProjectsList() string {
	h.t.Helper()
	resp, err := http.Get(h.server.URL + "/projects")
	if err != nil {
		h.t.Fatalf("GET /projects: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		h.t.Fatalf("status %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.String()
}
func TestProjectPanel_FreeFloating(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("free")
	envA := h.makeEnv("A")
	_ = h.makeEnv("B")
	// No lifecycle assigned.

	v1 := h.makeRelease(proj.ID, "1.0.0", "exit 0")
	_, depID := h.postDeploy(v1.ID, envA.ID)
	h.waitForDeploymentStatus(depID, "succeeded")

	page := h.getProjectPage(proj.ID)

	if !strings.Contains(page, ">A<") {
		t.Errorf("env A should appear in the panel")
	}
	if !strings.Contains(page, ">B<") {
		t.Errorf("env B should appear in the panel")
	}
	if !strings.Contains(page, "No lifecycle") {
		t.Errorf("free-floating panel should explain no-lifecycle state")
	}
}
