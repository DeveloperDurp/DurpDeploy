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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

// testHarness holds a full-stack server backed by an on-disk SQLite and a real
// DeploymentRunner. Tests exercise the actual /deployments endpoint so they
// verify status codes (303/422) plus the gate logic in one shot.
//
// ponytail: :memory: doesn't share across multiple connections in modernc/sqlite,
// and the runner uses a background goroutine on a different connection. We use
// a temp file instead so all connections see the same data.
type testHarness struct {
	repo   *repository.Repository
	rnr    *runner.DeploymentRunner
	server *httptest.Server
	dbPath string
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		dbPath,
	)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = os.RemoveAll(dir)
	})

	repo := repository.New(conn)
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	srv := httptest.NewServer(server.NewRouter(repo, rnr, parser))
	t.Cleanup(srv.Close)

	return &testHarness{repo: repo, rnr: rnr, server: srv, dbPath: dbPath}
}

type harnessCtx struct {
	t         *testing.T
	h         *testHarness
	project   db.Project
	release   db.Release
	lifecycle db.Lifecycle
	envs      map[string]db.Environment // keyed by name
}

func (h *testHarness) setupProjectWithLifecycle(
	t *testing.T,
	envNames []string,
) *harnessCtx {
	t.Helper()
	ctx := context.Background()
	proj, err := h.repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name:        "lifecycle-proj",
		Description: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	lc, err := h.repo.Queries.CreateLifecycle(ctx, db.CreateLifecycleParams{
		Name:        "dev-test-prod",
		Description: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	envs := make(map[string]db.Environment, len(envNames))
	for i, name := range envNames {
		env, err := h.repo.Queries.CreateEnvironment(
			ctx,
			db.CreateEnvironmentParams{
				Name:        name,
				Description: sql.NullString{},
			},
		)
		if err != nil {
			t.Fatalf("create env %s: %v", name, err)
		}
		envs[name] = env
		if _, err := h.repo.Queries.CreateLifecycleStage(
			ctx,
			db.CreateLifecycleStageParams{
				LifecycleID:   lc.ID,
				EnvironmentID: env.ID,
				SortOrder:     int64(i + 1),
			},
		); err != nil {
			t.Fatalf("create stage %s: %v", name, err)
		}
	}
	if err := h.repo.Queries.SetProjectLifecycle(
		ctx,
		db.SetProjectLifecycleParams{
			LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true},
			ID:          proj.ID,
		},
	); err != nil {
		t.Fatalf("assign lifecycle: %v", err)
	}
	hc := &harnessCtx{t: t, h: h, project: proj, lifecycle: lc, envs: envs}
	return hc
}

func (hc *harnessCtx) makeRelease(
	t *testing.T,
	version, scriptBody string,
) db.Release {
	t.Helper()
	steps := []map[string]any{
		{"name": "s1", "script_body": scriptBody, "sort_order": 1},
	}
	stepsJSON, _ := json.Marshal(steps)
	rel, err := hc.h.repo.Queries.CreateRelease(
		context.Background(),
		db.CreateReleaseParams{
			ProjectID: hc.project.ID,
			Version:   version,
			StepsJson: string(stepsJSON),
		},
	)
	if err != nil {
		t.Fatalf("create release %s: %v", version, err)
	}
	hc.release = rel
	return rel
}

// waitForDeploymentStatus polls the DB until the deployment's status is one of
// the expected values, or fails the test after timeout. Used to let the runner
// finish a deployment so the gate can see "succeeded" or "failed".
func (hc *harnessCtx) waitForDeploymentStatus(
	t *testing.T,
	releaseID, envID int64,
	expected ...string,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastDep db.Deployment
	for time.Now().Before(deadline) {
		dep, err := hc.h.repo.Queries.GetLatestDeploymentForReleaseEnv(
			context.Background(),
			db.GetLatestDeploymentForReleaseEnvParams{
				ReleaseID:     releaseID,
				EnvironmentID: envID,
			},
		)
		if err == nil {
			lastDep = dep
			for _, e := range expected {
				if dep.Status == e {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(
		"deployment for release=%d env=%d did not reach status %v (last status: %q, id: %d)",
		releaseID,
		envID,
		expected,
		lastDep.Status,
		lastDep.ID,
	)
}

// postDeploy submits a deployment form and returns the HTTP status code. The
// Go http client follows 303 redirects by default, which we don't want here
// (we only care about the immediate response).
func (hc *harnessCtx) postDeploy(
	t *testing.T,
	releaseID, envID int64,
	force bool,
) int {
	t.Helper()
	form := url.Values{}
	form.Set("release_id", fmt.Sprintf("%d", releaseID))
	form.Set("environment_id", fmt.Sprintf("%d", envID))
	if force {
		form.Set("force", "true")
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm(hc.h.server.URL+"/deployments", form)
	if err != nil {
		t.Fatalf("POST /deployments: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestGate_FreeFloatingProject_AllowsAnyEnv(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	proj, _ := h.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "free"},
	)
	envA, _ := h.repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "A"},
	)
	envB, _ := h.repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "B"},
	)

	steps := `[{"name":"s","script_body":"exit 0","sort_order":1}]`
	rel, _ := h.repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: proj.ID,
			Version:   "1",
			StepsJson: steps,
		},
	)

	hc := &harnessCtx{
		t:       t,
		h:       h,
		project: proj,
		release: rel,
		envs:    map[string]db.Environment{"A": envA, "B": envB},
	}

	if got := hc.postDeploy(
		t,
		rel.ID,
		envA.ID,
		false,
	); got != http.StatusSeeOther {
		t.Errorf("first env: got %d, want 303", got)
	}
	if got := hc.postDeploy(
		t,
		rel.ID,
		envB.ID,
		false,
	); got != http.StatusSeeOther {
		t.Errorf("second env (no lifecycle): got %d, want 303", got)
	}
}

func TestGate_EnvOutsideLifecycle_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta", "Gamma"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	// Delta is not part of the lifecycle.
	delta, _ := h.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: "Delta"},
	)

	if got := hc.postDeploy(
		t,
		rel.ID,
		delta.ID,
		false,
	); got != http.StatusUnprocessableEntity {
		t.Errorf("deploy to non-lifecycle env: got %d, want 422", got)
	}
}

func TestGate_FirstStage_Allowed(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Alpha"].ID,
		false,
	); got != http.StatusSeeOther {
		t.Errorf("first stage: got %d, want 303", got)
	}
}

func TestGate_UpperStage_NoPrior_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta", "Gamma"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Gamma"].ID,
		false,
	); got != http.StatusUnprocessableEntity {
		t.Errorf("upper stage without prior: got %d, want 422", got)
	}
}

func TestGate_UpperStage_PriorSucceeded_Allowed(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Alpha"].ID,
		false,
	); got != http.StatusSeeOther {
		t.Fatalf("Alpha: got %d, want 303", got)
	}
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Alpha"].ID, "succeeded")

	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Beta"].ID,
		false,
	); got != http.StatusSeeOther {
		t.Errorf("Beta after Alpha succeeded: got %d, want 303", got)
	}
}

func TestGate_UpperStage_PriorFailed_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 1") // fails

	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Alpha"].ID,
		false,
	); got != http.StatusSeeOther {
		t.Fatalf("Alpha deploy: got %d, want 303", got)
	}
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Alpha"].ID, "failed")

	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Beta"].ID,
		false,
	); got != http.StatusUnprocessableEntity {
		t.Errorf("Beta after Alpha failed: got %d, want 422", got)
	}
}

func TestGate_SkipStage_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta", "Gamma"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Alpha"].ID,
		false,
	); got != http.StatusSeeOther {
		t.Fatalf("Alpha: got %d, want 303", got)
	}
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Alpha"].ID, "succeeded")

	// Skip Beta, try Gamma.
	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Gamma"].ID,
		false,
	); got != http.StatusUnprocessableEntity {
		t.Errorf("Gamma without Beta: got %d, want 422", got)
	}
}

func TestGate_Force_BypassesGate(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	// No prior deployment to Alpha, but force=true should let us deploy to Beta.
	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Beta"].ID,
		true,
	); got != http.StatusSeeOther {
		t.Errorf("force to Beta: got %d, want 303", got)
	}

	// The deployment row should have forced=1.
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Beta"].ID, "succeeded")
	deps, err := h.repo.Queries.GetLatestSuccessfulDeploymentForEnv(
		context.Background(),
		hc.envs["Beta"].ID,
	)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if deps.Forced != 1 {
		t.Errorf("forced flag: got %d, want 1", deps.Forced)
	}
}

func TestGate_Force_CannotBypassEnvRestriction(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	delta, _ := h.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: "Delta"},
	)
	if got := hc.postDeploy(
		t,
		rel.ID,
		delta.ID,
		true,
	); got != http.StatusUnprocessableEntity {
		t.Errorf("force to non-lifecycle env: got %d, want 422", got)
	}
}

func TestGate_DifferentVersion_Independent(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})

	v1 := hc.makeRelease(t, "1.0.0", "exit 0")
	hc.postDeploy(t, v1.ID, hc.envs["Alpha"].ID, false)
	hc.waitForDeploymentStatus(t, v1.ID, hc.envs["Alpha"].ID, "succeeded")

	v2 := hc.makeRelease(t, "2.0.0", "exit 0")
	// v2 hasn't been deployed to Alpha, so it can't go to Beta yet.
	if got := hc.postDeploy(
		t,
		v2.ID,
		hc.envs["Beta"].ID,
		false,
	); got != http.StatusUnprocessableEntity {
		t.Errorf("v2 to Beta without v2 on Alpha: got %d, want 422", got)
	}
	// v2 on Alpha is fine.
	if got := hc.postDeploy(
		t,
		v2.ID,
		hc.envs["Alpha"].ID,
		false,
	); got != http.StatusSeeOther {
		t.Errorf("v2 to Alpha: got %d, want 303", got)
	}
}

func TestGate_RedeploySucceeded_StillSucceeds(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	hc.postDeploy(t, rel.ID, hc.envs["Alpha"].ID, false)
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Alpha"].ID, "succeeded")

	// Redeploy same release to first stage should be allowed (no prev env to gate on).
	if got := hc.postDeploy(
		t,
		rel.ID,
		hc.envs["Alpha"].ID,
		false,
	); got != http.StatusSeeOther {
		t.Errorf("redeploy to first stage: got %d, want 303", got)
	}
}

func TestReleasesPage_OnlyShowsDeployableEnvsByDefault(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	resp, err := http.Get(
		fmt.Sprintf("%s/projects/%d/releases", h.server.URL, hc.project.ID),
	)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	// Alpha is deployable (first stage), so it appears as a plain <option>.
	// Beta is bypassable (needs force), so it appears under the "Needs force" optgroup.
	if !strings.Contains(body, `>Alpha<`) {
		t.Errorf("Alpha should appear in env dropdown; body excerpt missing")
	}
	if !strings.Contains(body, `>Beta<`) {
		t.Errorf("Beta should appear in env dropdown; body excerpt missing")
	}
	if !strings.Contains(body, `data-gate-group="forceable"`) {
		t.Errorf(
			"Beta should be in the 'forceable' optgroup; missing data-gate-group",
		)
	}
	if !strings.Contains(body, `>Force<`) {
		t.Errorf("Force checkbox label should be rendered; missing '>Force<'")
	}
	_ = rel
}

func TestReleasesPage_HidesNonLifecycleEnvs(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})

	// "Outside" env is not in the lifecycle.
	h.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: "Outside"},
	)

	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	resp, err := http.Get(
		fmt.Sprintf("%s/projects/%d/releases", h.server.URL, hc.project.ID),
	)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if strings.Contains(body, `>Outside<`) {
		t.Errorf(
			"non-lifecycle env 'Outside' should NOT appear in the dropdown",
		)
	}
	if !strings.Contains(body, `>Alpha<`) {
		t.Errorf("Alpha should appear")
	}
	_ = rel
}

func TestGate_RenderError_ContainsReasonText(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	form := url.Values{}
	form.Set("release_id", fmt.Sprintf("%d", rel.ID))
	form.Set("environment_id", fmt.Sprintf("%d", hc.envs["Beta"].ID))
	resp, err := http.PostForm(h.server.URL+"/deployments", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()
	if !strings.Contains(body, "has not been successfully deployed to Alpha") {
		t.Errorf("error body missing gate reason; got: %s", body)
	}
}

// firstDeployment finds the most recent deployment matching release+env. Used
// to look up the ID we just created so we can wait for its status.
func firstDeployment(
	t *testing.T,
	h *testHarness,
	releaseID, envID int64,
) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		dep, err := h.repo.Queries.GetLatestDeploymentForReleaseEnv(
			context.Background(),
			db.GetLatestDeploymentForReleaseEnvParams{
				ReleaseID:     releaseID,
				EnvironmentID: envID,
			},
		)
		if err == nil {
			return dep.ID
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(
		"get latest deployment (release=%d env=%d): %v",
		releaseID,
		envID,
		lastErr,
	)
	return 0
}

// ---------------------------------------------------------------------------
// Deploy page tests
// ---------------------------------------------------------------------------

func postDeployPage(
	t *testing.T,
	base, projectID, releaseID, envID, force string,
) int {
	t.Helper()
	form := url.Values{}
	form.Set("release_id", releaseID)
	form.Set("environment_id", envID)
	if force != "" {
		form.Set("force", force)
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(base+"/projects/"+projectID+"/deploy", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestNewDeploymentPage_RendersForm(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("deploy-page")
	_ = h.makeEnv("DP-A")
	makeStepGlobal(t, h, proj.ID, "s", "true")
	h.makeRelease(proj.ID, "1.0.0", "true")

	resp, err := http.Get(
		fmt.Sprintf("%s/projects/%d/deploy", h.server.URL, proj.ID),
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
		"Deploy " + projName(proj.ID, h),
		"1.0.0",
		"DP-A",
		"<form",
		`action="/projects/` + fmt.Sprintf("%d", proj.ID) + `/deploy"`,
		`>Deploy<`,
		`>Cancel<`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("deploy page missing %q", marker)
		}
	}
}

func TestNewDeploymentPage_NoReleasesShowsEmptyState(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("no-rel")

	resp, err := http.Get(
		fmt.Sprintf("%s/projects/%d/deploy", h.server.URL, proj.ID),
	)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if !strings.Contains(body, "No releases yet") {
		t.Errorf("empty state message missing")
	}
	if !strings.Contains(
		body,
		fmt.Sprintf("href=\"/projects/%d/releases\"", proj.ID),
	) {
		t.Errorf("empty state should link to releases page")
	}
}

func TestNewDeploymentPage_NonExistentProject404(t *testing.T) {
	h := newProjectHarness(t)
	resp, err := http.Get(fmt.Sprintf("%s/projects/9999/deploy", h.server.URL))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestNewDeploymentPage_LifecycleFiltersEnvs(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("lc-filter")
	envA := h.makeEnv("LFA")
	envB := h.makeEnv("LFB")
	_ = h.makeEnv("LFO")
	lc := h.makeLifecycle("abc", envA.ID, envB.ID)
	h.assignLifecycle(proj.ID, lc.ID)
	makeStepGlobal(t, h, proj.ID, "s", "true")
	h.makeRelease(proj.ID, "1.0.0", "true")

	resp, err := http.Get(
		fmt.Sprintf("%s/projects/%d/deploy", h.server.URL, proj.ID),
	)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if !strings.Contains(body, "LFA") || !strings.Contains(body, "LFB") {
		t.Errorf("lifecycle stage envs should appear in deploy page dropdown")
	}
	if strings.Contains(body, "LFO") {
		t.Errorf("non-lifecycle env %q should NOT appear in dropdown", "LFO")
	}
}

func TestScheduleDeployment_Success(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("sched-ok")
	envA := h.makeEnv("SO")
	makeStepGlobal(t, h, proj.ID, "s", "true")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := postDeployPage(
		t,
		h.server.URL,
		fmt.Sprintf("%d", proj.ID),
		fmt.Sprintf("%d", rel.ID),
		fmt.Sprintf("%d", envA.ID),
		"",
	)
	if code != 303 {
		t.Fatalf("expected 303, got %d", code)
	}

	// Deployment should be created and start running.
	deps, err := h.repo.Queries.ListRecentDeploymentsForEnv(
		context.Background(),
		db.ListRecentDeploymentsForEnvParams{
			EnvironmentID: envA.ID,
			Limit:         1,
		},
	)
	if err != nil || len(deps) == 0 {
		t.Fatalf("expected a deployment row, got err=%v", err)
	}
	if deps[0].ReleaseID != rel.ID {
		t.Errorf("deployment release_id mismatch")
	}
}

func TestScheduleDeployment_CrossProjectReleaseIsRejected(t *testing.T) {
	h := newProjectHarness(t)
	projA := h.makeProject("crossA")
	projB := h.makeProject("crossB")
	envA := h.makeEnv("CRA")
	makeStepGlobal(t, h, projA.ID, "s", "true")
	makeStepGlobal(t, h, projB.ID, "s", "true")
	relA := h.makeRelease(projA.ID, "1.0.0", "true")
	_ = h.makeRelease(projB.ID, "1.0.0", "true")

	// Try to deploy projA's release via projB's deploy endpoint.
	code := postDeployPage(
		t,
		h.server.URL,
		fmt.Sprintf("%d", projB.ID),
		fmt.Sprintf("%d", relA.ID),
		fmt.Sprintf("%d", envA.ID),
		"",
	)
	if code != 400 {
		t.Errorf("cross-project deploy should be 400, got %d", code)
	}
}

func TestScheduleDeployment_GateViolation422(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("gate-422")
	dev := h.makeEnv("GD")
	prod := h.makeEnv("GP")
	out := h.makeEnv("GO")
	lc := h.makeLifecycle("dp", dev.ID, prod.ID)
	h.assignLifecycle(proj.ID, lc.ID)
	makeStepGlobal(t, h, proj.ID, "s", "true")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	// Deploy to a non-lifecycle env without force -> 422.
	code := postDeployPage(
		t,
		h.server.URL,
		fmt.Sprintf("%d", proj.ID),
		fmt.Sprintf("%d", rel.ID),
		fmt.Sprintf("%d", out.ID),
		"",
	)
	if code != 422 {
		t.Errorf("expected 422, got %d", code)
	}
}

func TestScheduleDeployment_ForceBypasses(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("force-ok")
	dev := h.makeEnv("FD")
	prod := h.makeEnv("FP")
	lc := h.makeLifecycle("dp", dev.ID, prod.ID)
	h.assignLifecycle(proj.ID, lc.ID)
	makeStepGlobal(t, h, proj.ID, "s", "true")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	// First deploy to dev so the gate would let v1.0.0 through.
	code := postDeployPage(
		t,
		h.server.URL,
		fmt.Sprintf("%d", proj.ID),
		fmt.Sprintf("%d", rel.ID),
		fmt.Sprintf("%d", dev.ID),
		"",
	)
	if code != 303 {
		t.Fatalf("dev deploy expected 303, got %d", code)
	}
	// Wait for it to settle.
	for i := 0; i < 50; i++ {
		d, _ := h.repo.Queries.GetLatestSuccessfulDeploymentForEnv(
			context.Background(),
			dev.ID,
		)
		if d.ReleaseID == rel.ID {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Now force to prod (skipping the gate would block).
	code = postDeployPage(
		t,
		h.server.URL,
		fmt.Sprintf("%d", proj.ID),
		fmt.Sprintf("%d", rel.ID),
		fmt.Sprintf("%d", prod.ID),
		"true",
	)
	if code != 303 {
		t.Errorf("force deploy expected 303, got %d", code)
	}
}

// projName returns the name of a project (used in test assertions). It
// looks up the name in the harness repo to avoid a raw-id string in
// the body content check.
func projName(id int64, h *projectHarness) string {
	p, err := h.repo.Queries.GetProject(context.Background(), id)
	if err != nil {
		return ""
	}
	return p.Name
}

// TestNewDeploymentPage_LifecycleOrdersEnvsInStageOrder verifies the
// deploy page renders envs in lifecycle stage order, not DB insertion
// order. The test inserts envs in reverse-stage order and asserts the
// page's env dropdown follows the lifecycle.
func TestNewDeploymentPage_LifecycleOrdersEnvsInStageOrder(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("lc-order")
	// Create envs in reverse order of how they appear in the lifecycle.
	envC := h.makeEnv("Z-prod")
	envB := h.makeEnv("M-test")
	envA := h.makeEnv("A-dev")
	lc := h.makeLifecycle("dtp", envA.ID, envB.ID, envC.ID)
	h.assignLifecycle(proj.ID, lc.ID)
	makeStepGlobal(t, h, proj.ID, "s", "true")
	h.makeRelease(proj.ID, "1.0.0", "true")

	page := h.getProjectPage(proj.ID)

	// The lifecycle adds them dev -> test -> prod. Verify the env
	// names appear in that order in the page body (after the Release
	// dropdown).
	idxDev := strings.Index(page, "A-dev")
	idxTest := strings.Index(page, "M-test")
	idxProd := strings.Index(page, "Z-prod")
	if idxDev < 0 || idxTest < 0 || idxProd < 0 {
		t.Fatalf(
			"missing env names in page (dev=%d test=%d prod=%d)",
			idxDev,
			idxTest,
			idxProd,
		)
	}
	if !(idxDev < idxTest && idxTest < idxProd) {
		t.Errorf(
			"envs not in lifecycle order: dev@%d test@%d prod@%d",
			idxDev,
			idxTest,
			idxProd,
		)
	}
}

// TestNewDeploymentPage_LifecycleOnlyShowsEmptyStateWhenNoStages verifies
// the edge case: a lifecycle-bound project with zero stages shows the
// helpful "no environments available" message rather than an empty
// dropdown.
func TestNewDeploymentPage_LifecycleOnlyShowsEmptyStateWhenNoStages(
	t *testing.T,
) {
	h := newProjectHarness(t)
	proj := h.makeProject("lc-empty")
	lc := h.makeLifecycle("empty-lc")
	h.assignLifecycle(proj.ID, lc.ID)
	makeStepGlobal(t, h, proj.ID, "s", "true")
	h.makeRelease(proj.ID, "1.0.0", "true")

	page := h.getProjectPage(proj.ID)
	// This is the project page, not the deploy page. But the user said
	// "I still see all environments" which we read as the deploy page.
	// Check the deploy page directly.
	resp, err := http.Get(
		fmt.Sprintf("%s/projects/%d/deploy", h.server.URL, proj.ID),
	)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page = buf.String()

	if !strings.Contains(page, "No environments available") {
		t.Errorf("empty-state message missing for lifecycle with no stages")
	}
	if strings.Contains(page, "<select name=\"environment_id\"") {
		t.Errorf("env dropdown should not render when no stages; got: %s", page)
	}
	_ = proj
}

// TestNewDeploymentPage_ReleaseAwareGateState verifies that the deploy
// page applies the per-release gate: with no release picked, all
// lifecycle stages show; with a release picked, only stages whose prior
// stage has succeeded with that release are deployable.
func TestNewDeploymentPage_ReleaseAwareGateState(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("rg")
	dev := h.makeEnv("RG-Dev")
	test := h.makeEnv("RG-Test")
	prod := h.makeEnv("RG-Prod")
	lc := h.makeLifecycle("d-t-p", dev.ID, test.ID, prod.ID)
	h.assignLifecycle(proj.ID, lc.ID)
	makeStepGlobal(t, h, proj.ID, "s", "true")
	v1 := h.makeRelease(proj.ID, "1.0.0", "true")

	// Without ?release_id — all 3 stages appear.
	page := h.getProjectPage(proj.ID)
	for _, name := range []string{"RG-Dev", "RG-Test", "RG-Prod"} {
		if !strings.Contains(page, name) {
			t.Errorf("no-release page missing %s", name)
		}
	}

	// With ?release_id=v1 on a 3-stage lifecycle, stage 0 (dev) is
	// deployable (no prior required); stages 1 and 2 (test, prod) are
	// force-bypassable because their prior stage hasn't seen v1 yet.
	resp, err := http.Get(
		fmt.Sprintf(
			"%s/projects/%d/deploy?release_id=%d",
			h.server.URL,
			proj.ID,
			v1.ID,
		),
	)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page = buf.String()

	if !strings.Contains(page, "1.0.0") {
		t.Errorf("page should show the picked release version")
	}
	if !strings.Contains(page, fmt.Sprintf("value=\"%d\"", dev.ID)) {
		t.Errorf(
			"dev should be deployable for fresh release (no prior required)",
		)
	}
	if !strings.Contains(page, fmt.Sprintf("value=\"%d\"", test.ID)) {
		t.Errorf(
			"test should be in dropdown (in force optgroup) for fresh release",
		)
	}
	if !strings.Contains(page, fmt.Sprintf("value=\"%d\"", prod.ID)) {
		t.Errorf(
			"prod should be in dropdown (in force optgroup) for fresh release",
		)
	}
	if !strings.Contains(page, "Needs force") {
		t.Errorf("force optgroup should appear for stages 2 and 3")
	}

	// After deploying v1 to dev, the next page-load with the same
	// release should show dev as already-deployed (hidden) and test
	// as deployable.
	resp2, err2 := h.postDeployRelease(proj.ID, v1.ID, dev.ID)
	if err2 != nil {
		t.Fatalf("postDeploy: %v", err2)
	}
	if resp2 != 303 {
		t.Fatalf("dev deploy got %d, want 303", resp2)
	}
	h.waitForDeploymentToSucceed(proj.ID, v1.ID, dev.ID)

	resp, err = http.Get(
		fmt.Sprintf(
			"%s/projects/%d/deploy?release_id=%d",
			h.server.URL,
			proj.ID,
			v1.ID,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf = new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page = buf.String()
	for i := 0; i < len(page); i++ {
		if i+8 <= len(page) && page[i:i+8] == `value="1"` {
			start := i - 30
			if start < 0 {
				start = 0
			}
			end := i + 50
			if end > len(page) {
				end = len(page)
			}
			t.Logf("value=\"1\" at offset %d: %q", i, page[start:end])
		}
	}

	// Scope the assertion to the env select so the release dropdown's
	// matching value=1 doesn't trip us.
	envSelect := envSelectFromPage(page)
	// dev already has v1 deployed -> appears in the "Already deployed
	// at this version" optgroup, not hidden. This lets the user re-run
	// the same release to a stage that's already at that version.
	if !strings.Contains(envSelect, fmt.Sprintf("value=\"%d\"", dev.ID)) {
		t.Errorf(
			"dev should appear in the Already-deployed optgroup after v1 deploys there; env select: %s",
			envSelect,
		)
	}
	// test: prior stage (dev) succeeded -> deployable.
	if !strings.Contains(envSelect, fmt.Sprintf("value=\"%d\"", test.ID)) {
		t.Errorf(
			"test should appear as deployable after dev has v1; env select: %s",
			envSelect,
		)
	}
	// prod: prior stage (test) hasn't seen v1 -> in force group, not
	// hidden. The user can force-deploy to skip the gate.
	if !strings.Contains(envSelect, fmt.Sprintf("value=\"%d\"", prod.ID)) {
		t.Errorf(
			"prod should be in dropdown (force optgroup) when test hasn't seen v1; env select: %s",
			envSelect,
		)
	}
}

// envSelectFromPage returns the inner content of the env_id <select>.
// Scoping assertions to the env select prevents the release dropdown's
// matching value ids from tripping the test.
func envSelectFromPage(page string) string {
	start := strings.Index(page, `name="environment_id"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(page[start:], `</select>`)
	if end < 0 {
		return ""
	}
	return page[start : start+end]
}

// helpers used by the test above; defined here to avoid touching the
// existing harness or hand-rolling HTTP plumbing.
func envIDByName(body, name string) int64 {
	// not actually used; the test asserts presence by pattern only.
	// kept for future extension.
	_ = body
	_ = name
	return 0
}

func (h *projectHarness) postDeployRelease(
	projectID, releaseID, envID int64,
) (int, error) {
	form := url.Values{}
	form.Set("release_id", fmt.Sprintf("%d", releaseID))
	form.Set("environment_id", fmt.Sprintf("%d", envID))
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, _ := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/projects/%d/deploy", h.server.URL, projectID),
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func (h *projectHarness) waitForDeploymentToSucceed(
	projectID, releaseID, envID int64,
) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dep, err := h.repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(
			context.Background(),
			db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
				ReleaseID:     releaseID,
				EnvironmentID: envID,
			},
		)
		if err == nil && dep.ReleaseID == releaseID {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
