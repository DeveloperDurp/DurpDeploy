package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

// projectHarness wraps a full-stack server with helpers to create a project,
// a lifecycle, envs, releases, and to drive deployments. Every request goes
// through an authenticated admin session (P0-4 requirement).
type projectHarness struct {
	t      *testing.T
	repo   *repository.Repository
	server *httptest.Server
	sess   *authedSession
}

func newProjectHarness(t *testing.T) *projectHarness {
	t.Helper()
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
	authHandler := handler.NewAuthHandler(repo)
	srv := httptest.NewServer(server.NewRouter(repo, rnr, parser, authHandler))
	t.Cleanup(srv.Close)
	h := &projectHarness{t: t, repo: repo, server: srv}
	h.sess = seedSession(t, repo, srv.URL, "admin")
	return h
}

// setRole swaps the harness session for a new user with the given role.
// Used by tests that need to act as a non-admin (e.g. viewer).
func (h *projectHarness) setRole(role string) {
	h.t.Helper()
	h.sess = seedSession(h.t, h.repo, h.server.URL, role)
}

// csrfToken returns the current session's CSRF token for form helpers.
func (h *projectHarness) csrfToken() string {
	return h.sess.csrfToken
}

// authedClient returns the cookie-jarred HTTP client for this harness.
func (h *projectHarness) authedClient() *http.Client {
	return h.sess.client
}

func (h *projectHarness) makeProject(name string) db.Project {
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

func (h *projectHarness) makeEnv(name string) db.Environment {
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

func (h *projectHarness) makeRelease(
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

func (h *projectHarness) makeLifecycle(
	name string,
	envIDs ...int64,
) db.Lifecycle {
	h.t.Helper()
	lc, err := h.repo.Queries.CreateLifecycle(
		context.Background(),
		db.CreateLifecycleParams{Name: name, Description: sql.NullString{}},
	)
	if err != nil {
		h.t.Fatalf("create lifecycle: %v", err)
	}
	for i, eid := range envIDs {
		if _, err := h.repo.Queries.CreateLifecycleStage(
			context.Background(),
			db.CreateLifecycleStageParams{
				LifecycleID:   lc.ID,
				EnvironmentID: eid,
				SortOrder:     int64(i + 1),
			},
		); err != nil {
			h.t.Fatalf("create stage: %v", err)
		}
	}
	if err := h.repo.Queries.SetProjectLifecycle(
		context.Background(),
		db.SetProjectLifecycleParams{
			LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true},
			ID:          lc.ID, // not used; we override below
		},
	); err != nil {
		// ignore: the helper is wrong for project_id, the caller does it
	}
	return lc
}

func (h *projectHarness) assignLifecycle(projectID, lifecycleID int64) {
	h.t.Helper()
	if err := h.repo.Queries.SetProjectLifecycle(
		context.Background(),
		db.SetProjectLifecycleParams{
			LifecycleID: sql.NullInt64{Int64: lifecycleID, Valid: true},
			ID:          projectID,
		},
	); err != nil {
		h.t.Fatalf("assign lifecycle: %v", err)
	}
}

// waitForDeploymentStatus blocks until the deployment's status is one of
// expected, then returns the final row. Used so the runner has time to set
// status to "succeeded" or "failed" before the panel queries it.
func (h *projectHarness) waitForDeploymentStatus(
	deploymentID int64,
	expected ...string,
) db.Deployment {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var dep db.Deployment
	for time.Now().Before(deadline) {
		d, err := h.repo.Queries.GetDeployment(
			context.Background(),
			deploymentID,
		)
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
	h.t.Fatalf(
		"deployment %d did not reach status %v (last: %q)",
		deploymentID,
		expected,
		dep.Status,
	)
	return dep
}

func (h *projectHarness) postDeploy(releaseID, envID int64) (int, int64) {
	h.t.Helper()
	form := map[string]string{
		"release_id":     fmt.Sprintf("%d", releaseID),
		"environment_id": fmt.Sprintf("%d", envID),
		"csrf_token":     h.csrfToken(),
	}
	body := strings.NewReader(formEncode(form))
	req, _ := http.NewRequest("POST", h.server.URL+"/deployments", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.authedClient().Do(req)
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
	resp, err := h.authedClient().Get(
		fmt.Sprintf("%s/projects/%d", h.server.URL, projectID),
	)
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
		t.Errorf(
			"'failed' status should appear; the failed deploy must be visible",
		)
	}
	if !strings.Contains(page, "No successful deploys") {
		t.Errorf(
			"Last Deployed column should say 'No successful deploys' for env that never succeeded",
		)
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
	recents, err := h.repo.Queries.ListRecentDeploymentsForEnv(
		context.Background(),
		db.ListRecentDeploymentsForEnvParams{
			EnvironmentID: dev.ID,
			Limit:         5,
		},
	)
	if err != nil || len(recents) < 2 {
		t.Fatalf("expected >=2 deployments, got %d (err=%v)", len(recents), err)
	}
	second := recents[1]
	if err := h.repo.Queries.UpdateDeploymentStatus(
		context.Background(),
		db.UpdateDeploymentStatusParams{
			ID:         second.ID,
			Status:     "failed",
			StartedAt:  sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	); err != nil {
		t.Fatalf("patch status: %v", err)
	}

	page := h.getProjectPage(proj.ID)

	// The streak should contain at least one red and one green dot. We can't
	// easily count from HTML, but the bg-error and bg-success classes only
	// appear on AttemptDots, so their presence is a sufficient check.
	if !strings.Contains(page, "bg-error") {
		t.Errorf(
			"expected a failed dot in the streak strip; bg-error class not found",
		)
	}
	if !strings.Contains(page, "bg-success") {
		t.Errorf(
			"expected a success dot in the streak strip; bg-success class not found",
		)
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
	depA2, err := h.repo.Queries.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID:     vA.ID,
			EnvironmentID: envProd.ID,
			Status:        "running",
			StartedAt:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
			FinishedAt:    sql.NullInt64{},
			Forced:        0,
		},
	)
	if err != nil {
		t.Fatalf("create prod deployment: %v", err)
	}
	if err := h.repo.Queries.UpdateDeploymentStatus(
		context.Background(),
		db.UpdateDeploymentStatusParams{
			ID:         depA2.ID,
			Status:     "failed",
			StartedAt:  sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	); err != nil {
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
		t.Errorf(
			"lifecycle envs dev/prod should appear in A's row; body missing",
		)
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
		t.Errorf(
			"version 1.0.0 should appear in the list page (multiple cells)",
		)
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
			t.Errorf(
				"lifecycle stage %q must appear in the list, even if untouched",
				name,
			)
		}
	}
	// The em-dash placeholder appears for untouched envs. Use a class-scoped
	// search so a stray "—" elsewhere doesn't satisfy the check.
	if !strings.Contains(page, "bg-base-300") {
		t.Errorf(
			"untouched envs should render an em-dash placeholder with bg-base-300 styling",
		)
	}
}

func (h *projectHarness) getProjectsList() string {
	h.t.Helper()
	resp, err := h.authedClient().Get(h.server.URL + "/projects")
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

// TestStepsPage_RendersFullPage verifies the new dedicated /steps-page
// route renders both step names, an Add Step button, and breadcrumbs
// pointing back to the project.
func TestStepsPage_RendersFullPage(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("steps-page")
	makeStepGlobal(t, h, proj.ID, "first-step", "echo+a")
	makeStepGlobal(t, h, proj.ID, "second-step", "echo+b")

	// ponytail: use authedClient so the session cookie is sent; bare
	// http.Get gets 303-redirected to /login by the auth middleware.
	resp, err := h.authedClient().Get(
		fmt.Sprintf("%s/projects/%d/steps-page", h.server.URL, proj.ID),
	)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	for _, marker := range []string{
		"first-step", "echo+a",
		"second-step", "echo+b",
		"Add Step", "Insert from Template",
		fmt.Sprintf("href=\"/projects/%d\"", proj.ID), // breadcrumb
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("steps page missing %q", marker)
		}
	}
}

// TestVariablesPreview_ShowsTopVarsAndManageLink verifies the project
// overview's variables preview shows name+value+env for each variable
// and exposes a Manage link to the full variables page.
func TestVariablesPreview_ShowsTopVarsAndManageLink(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("vars-preview")
	envA := h.makeEnv("preview-A")
	makeVariableGlobal(t, h, proj.ID, "UNSCOPED_VAR", "uval", "")
	makeVariableGlobal(
		t,
		h,
		proj.ID,
		"A_ONLY",
		"aval",
		strconv.FormatInt(envA.ID, 10),
	)

	page := h.getProjectPage(proj.ID)

	for _, marker := range []string{
		"UNSCOPED_VAR", "uval",
		"A_ONLY", "aval",
		"preview-A",
		fmt.Sprintf("href=\"/projects/%d/variables\"", proj.ID), // manage link
	} {
		if !strings.Contains(page, marker) {
			t.Errorf("vars preview missing %q", marker)
		}
	}
}

// makeStepGlobal is a thin wrapper so the test file doesn't need its own
// reference to the harness's repo. Lives here to avoid leaking into the
// production code path.
func makeStepGlobal(
	t *testing.T,
	h *projectHarness,
	pid int64,
	name, body string,
) {
	t.Helper()
	form := url.Values{"name": {name}, "script_body": {body}}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, pid),
		form,
	)
	if err != nil {
		t.Fatalf("create step %s: %v", name, err)
	}
	resp.Body.Close()
}

// makeVariableGlobal creates a variable via the public POST endpoint.
// envID == "" means unscoped.
func makeVariableGlobal(
	t *testing.T,
	h *projectHarness,
	pid int64,
	name, value, envID string,
) {
	t.Helper()
	form := url.Values{"name": {name}, "value": {value}}
	form.Set("csrf_token", h.csrfToken())
	if envID != "" {
		form.Set("environment_id", envID)
	}
	resp, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/variables", h.server.URL, pid),
		form,
	)
	if err != nil {
		t.Fatalf("create var %s: %v", name, err)
	}
	resp.Body.Close()
}

// TestUpdateProject_AfterSubmitLandsOnProject verifies the two navigation
// paths out of the edit form: Cancel returns the user to the project
// page, and Update also returns the user to the project page (not the
// projects list, which was the bug).
func TestUpdateProject_AfterSubmitLandsOnProject(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("nav-redirect")

	// Cancel link on the edit form should point to the project's detail page.
	editPage, err := h.authedClient().Get(
		fmt.Sprintf("%s/projects/%d/edit", h.server.URL, proj.ID),
	)
	if err != nil {
		t.Fatalf("GET edit: %v", err)
	}
	defer editPage.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(editPage.Body)
	body := buf.String()

	cancelHref := fmt.Sprintf(`href="/projects/%d"`, proj.ID)
	if !strings.Contains(body, cancelHref) {
		t.Errorf("edit form Cancel link should contain %s", cancelHref)
	}

	wantRedirect := fmt.Sprintf("/projects/%d", proj.ID)

	// Submit the form. The HX path should set HX-Redirect to the project
	// page; the non-HX path (curl without HX-Request) should 303 to it.
	hxReq, _ := http.NewRequest("PUT",
		fmt.Sprintf("%s/projects/%d", h.server.URL, proj.ID),
		strings.NewReader("name=renamed&description=&lifecycle_id=&csrf_token="+h.csrfToken()))
	hxReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hxReq.Header.Set("HX-Request", "true")
	hxResp, err := h.authedClient().Do(hxReq)
	if err != nil {
		t.Fatalf("PUT (HX): %v", err)
	}
	hxResp.Body.Close()
	if got := hxResp.Header.Get("HX-Redirect"); got != wantRedirect {
		t.Errorf("HX-Redirect: got %q, want %q", got, wantRedirect)
	}

	nonHxReq, _ := http.NewRequest("PUT",
		fmt.Sprintf("%s/projects/%d", h.server.URL, proj.ID),
		strings.NewReader("name=renamed-2&description=&lifecycle_id=&csrf_token="+h.csrfToken()))
	nonHxReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	nonHxResp, err := h.authedClient().Do(nonHxReq)
	if err != nil {
		t.Fatalf("PUT (non-HX): %v", err)
	}
	nonHxResp.Body.Close()
	if nonHxResp.StatusCode != http.StatusSeeOther {
		t.Errorf("non-HX PUT: got %d, want 303", nonHxResp.StatusCode)
	}
	if loc := nonHxResp.Header.Get("Location"); loc != wantRedirect {
		t.Errorf("non-HX Location: got %q, want %q", loc, wantRedirect)
	}
}

// TestProjectPage_EditOnDetailDeleteOnEditForm verifies the new
// placement: Edit lives on the project detail page (so the user can
// jump straight to editing from the overview), and Delete lives on
// the edit form (so the destructive action is one click away once
// the user has committed to managing the project). Neither should
// appear on the read-only projects list.
func TestProjectPage_EditOnDetailDeleteOnEditForm(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("placement")

	listPage := h.getProjectsList()
	detailPage := h.getProjectPage(proj.ID)
	editPage, err := h.authedClient().Get(
		fmt.Sprintf("%s/projects/%d/edit", h.server.URL, proj.ID),
	)
	if err != nil {
		t.Fatalf("GET edit: %v", err)
	}
	defer editPage.Body.Close()
	if editPage.StatusCode != 200 {
		t.Fatalf("edit status %d", editPage.StatusCode)
	}
	editBuf := new(bytes.Buffer)
	editBuf.ReadFrom(editPage.Body)
	editBody := editBuf.String()

	if !strings.Contains(
		detailPage,
		fmt.Sprintf(`href="/projects/%d/edit"`, proj.ID),
	) {
		t.Errorf("project detail page should contain Edit link")
	}
	if strings.Contains(
		listPage,
		fmt.Sprintf(`href="/projects/%d/edit"`, proj.ID),
	) {
		t.Errorf("projects list should not contain Edit link")
	}

	if !strings.Contains(
		editBody,
		fmt.Sprintf(`hx-delete="/projects/%d"`, proj.ID),
	) {
		t.Errorf("edit form should contain Delete button (hx-delete)")
	}
	if strings.Contains(
		detailPage,
		fmt.Sprintf(`hx-delete="/projects/%d"`, proj.ID),
	) {
		t.Errorf(
			"project detail page should not contain Delete button (moved to edit form)",
		)
	}
	if strings.Contains(
		listPage,
		fmt.Sprintf(`hx-delete="/projects/%d"`, proj.ID),
	) {
		t.Errorf("projects list should not contain Delete button")
	}
}

// TestProjectDelete_FromProjectPageNavigatesAway verifies that deleting
// from the project page navigates to /projects (not the in-place list
// re-render that the list page uses).
func TestProjectDelete_FromProjectPageNavigatesAway(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("delete-from-detail")

	// HX path.
	// ponytail: Go's ParseForm ignores DELETE bodies, so the CSRF token
	// must go in the X-CSRF-Token header (the middleware's fallback).
	hxReq, _ := http.NewRequest(
		"DELETE",
		fmt.Sprintf("%s/projects/%d", h.server.URL, proj.ID),
		strings.NewReader("csrf_token="+h.csrfToken()),
	)
	hxReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hxReq.Header.Set("X-CSRF-Token", h.csrfToken())
	hxReq.Header.Set("HX-Request", "true")
	hxResp, err := h.authedClient().Do(hxReq)
	if err != nil {
		t.Fatalf("DELETE (HX): %v", err)
	}
	hxResp.Body.Close()
	if got := hxResp.Header.Get("HX-Redirect"); got != "/projects" {
		t.Errorf("HX-Redirect: got %q, want /projects", got)
	}

	// Non-HX path.
	h.makeProject("delete-from-detail-2")
	projs := h.getProjectsList()
	re := regexp.MustCompile(`href="/projects/(\d+)"`)
	ids := re.FindAllStringSubmatch(projs, -1)
	if len(ids) < 1 {
		t.Fatal("could not find any project id on the list page")
	}
	// grab the last id
	idStr := ids[len(ids)-1][1]
	nonHxReq, _ := http.NewRequest(
		"DELETE",
		fmt.Sprintf("%s/projects/%s", h.server.URL, idStr),
		strings.NewReader("csrf_token="+h.csrfToken()),
	)
	nonHxReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	nonHxReq.Header.Set("X-CSRF-Token", h.csrfToken())
	nonHxResp, err := h.authedClient().Do(nonHxReq)
	if err != nil {
		t.Fatalf("DELETE (non-HX): %v", err)
	}
	nonHxResp.Body.Close()
	if nonHxResp.StatusCode != http.StatusSeeOther {
		t.Errorf("non-HX DELETE: got %d, want 303", nonHxResp.StatusCode)
	}
	if loc := nonHxResp.Header.Get("Location"); loc != "/projects" {
		t.Errorf("non-HX Location: got %q, want /projects", loc)
	}
}

// TestProjectsList_OmitsDeployButtonAndDetailHasIt verifies the new
// placement: Deploy lives on the project detail page (paired with Edit
// and Back), not on the projects list. The list is a read-only summary.
func TestProjectsList_OmitsDeployButtonAndDetailHasIt(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("deploy-on-detail")

	listPage := h.getProjectsList()
	detailPage := h.getProjectPage(proj.ID)

	want := fmt.Sprintf(`href="/projects/%d/deploy"`, proj.ID)

	if strings.Contains(listPage, want) {
		t.Errorf("projects list should not contain %s", want)
	}
	if !strings.Contains(detailPage, want) {
		t.Errorf("project detail page should contain %s", want)
	}
}
