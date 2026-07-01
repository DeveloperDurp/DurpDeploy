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
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", dbPath)
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
	srv := httptest.NewServer(server.NewRouter(repo, rnr))
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

func (h *testHarness) setupProjectWithLifecycle(t *testing.T, envNames []string) *harnessCtx {
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
		env, err := h.repo.Queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{
			Name:        name,
			Description: sql.NullString{},
		})
		if err != nil {
			t.Fatalf("create env %s: %v", name, err)
		}
		envs[name] = env
		if _, err := h.repo.Queries.CreateLifecycleStage(ctx, db.CreateLifecycleStageParams{
			LifecycleID:   lc.ID,
			EnvironmentID: env.ID,
			SortOrder:     int64(i + 1),
		}); err != nil {
			t.Fatalf("create stage %s: %v", name, err)
		}
	}
	if err := h.repo.Queries.SetProjectLifecycle(ctx, db.SetProjectLifecycleParams{
		LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true},
		ID:          proj.ID,
	}); err != nil {
		t.Fatalf("assign lifecycle: %v", err)
	}
	hc := &harnessCtx{t: t, h: h, project: proj, lifecycle: lc, envs: envs}
	return hc
}

func (hc *harnessCtx) makeRelease(t *testing.T, version, scriptBody string) db.Release {
	t.Helper()
	steps := []map[string]any{
		{"name": "s1", "script_body": scriptBody, "sort_order": 1},
	}
	stepsJSON, _ := json.Marshal(steps)
	rel, err := hc.h.repo.Queries.CreateRelease(context.Background(), db.CreateReleaseParams{
		ProjectID: hc.project.ID,
		Version:   version,
		StepsJson: string(stepsJSON),
	})
	if err != nil {
		t.Fatalf("create release %s: %v", version, err)
	}
	hc.release = rel
	return rel
}

// waitForDeploymentStatus polls the DB until the deployment's status is one of
// the expected values, or fails the test after timeout. Used to let the runner
// finish a deployment so the gate can see "succeeded" or "failed".
func (hc *harnessCtx) waitForDeploymentStatus(t *testing.T, releaseID, envID int64, expected ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastDep db.Deployment
	for time.Now().Before(deadline) {
		dep, err := hc.h.repo.Queries.GetLatestDeploymentForReleaseEnv(context.Background(), db.GetLatestDeploymentForReleaseEnvParams{
			ReleaseID:     releaseID,
			EnvironmentID: envID,
		})
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
	t.Fatalf("deployment for release=%d env=%d did not reach status %v (last status: %q, id: %d)", releaseID, envID, expected, lastDep.Status, lastDep.ID)
}

// postDeploy submits a deployment form and returns the HTTP status code. The
// Go http client follows 303 redirects by default, which we don't want here
// (we only care about the immediate response).
func (hc *harnessCtx) postDeploy(t *testing.T, releaseID, envID int64, force bool) int {
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

	proj, _ := h.repo.Queries.CreateProject(ctx, db.CreateProjectParams{Name: "free"})
	envA, _ := h.repo.Queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{Name: "A"})
	envB, _ := h.repo.Queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{Name: "B"})

	steps := `[{"name":"s","script_body":"exit 0","sort_order":1}]`
	rel, _ := h.repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{ProjectID: proj.ID, Version: "1", StepsJson: steps})

	hc := &harnessCtx{t: t, h: h, project: proj, release: rel, envs: map[string]db.Environment{"A": envA, "B": envB}}

	if got := hc.postDeploy(t, rel.ID, envA.ID, false); got != http.StatusSeeOther {
		t.Errorf("first env: got %d, want 303", got)
	}
	if got := hc.postDeploy(t, rel.ID, envB.ID, false); got != http.StatusSeeOther {
		t.Errorf("second env (no lifecycle): got %d, want 303", got)
	}
}

func TestGate_EnvOutsideLifecycle_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta", "Gamma"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	// Delta is not part of the lifecycle.
	delta, _ := h.repo.Queries.CreateEnvironment(context.Background(), db.CreateEnvironmentParams{Name: "Delta"})

	if got := hc.postDeploy(t, rel.ID, delta.ID, false); got != http.StatusUnprocessableEntity {
		t.Errorf("deploy to non-lifecycle env: got %d, want 422", got)
	}
}

func TestGate_FirstStage_Allowed(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(t, rel.ID, hc.envs["Alpha"].ID, false); got != http.StatusSeeOther {
		t.Errorf("first stage: got %d, want 303", got)
	}
}

func TestGate_UpperStage_NoPrior_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta", "Gamma"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(t, rel.ID, hc.envs["Gamma"].ID, false); got != http.StatusUnprocessableEntity {
		t.Errorf("upper stage without prior: got %d, want 422", got)
	}
}

func TestGate_UpperStage_PriorSucceeded_Allowed(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(t, rel.ID, hc.envs["Alpha"].ID, false); got != http.StatusSeeOther {
		t.Fatalf("Alpha: got %d, want 303", got)
	}
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Alpha"].ID, "succeeded")

	if got := hc.postDeploy(t, rel.ID, hc.envs["Beta"].ID, false); got != http.StatusSeeOther {
		t.Errorf("Beta after Alpha succeeded: got %d, want 303", got)
	}
}

func TestGate_UpperStage_PriorFailed_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 1") // fails

	if got := hc.postDeploy(t, rel.ID, hc.envs["Alpha"].ID, false); got != http.StatusSeeOther {
		t.Fatalf("Alpha deploy: got %d, want 303", got)
	}
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Alpha"].ID, "failed")

	if got := hc.postDeploy(t, rel.ID, hc.envs["Beta"].ID, false); got != http.StatusUnprocessableEntity {
		t.Errorf("Beta after Alpha failed: got %d, want 422", got)
	}
}

func TestGate_SkipStage_Blocked(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta", "Gamma"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	if got := hc.postDeploy(t, rel.ID, hc.envs["Alpha"].ID, false); got != http.StatusSeeOther {
		t.Fatalf("Alpha: got %d, want 303", got)
	}
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Alpha"].ID, "succeeded")

	// Skip Beta, try Gamma.
	if got := hc.postDeploy(t, rel.ID, hc.envs["Gamma"].ID, false); got != http.StatusUnprocessableEntity {
		t.Errorf("Gamma without Beta: got %d, want 422", got)
	}
}

func TestGate_Force_BypassesGate(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	// No prior deployment to Alpha, but force=true should let us deploy to Beta.
	if got := hc.postDeploy(t, rel.ID, hc.envs["Beta"].ID, true); got != http.StatusSeeOther {
		t.Errorf("force to Beta: got %d, want 303", got)
	}

	// The deployment row should have forced=1.
	hc.waitForDeploymentStatus(t, rel.ID, hc.envs["Beta"].ID, "succeeded")
	deps, err := h.repo.Queries.GetLatestSuccessfulDeploymentForEnv(context.Background(), hc.envs["Beta"].ID)
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

	delta, _ := h.repo.Queries.CreateEnvironment(context.Background(), db.CreateEnvironmentParams{Name: "Delta"})
	if got := hc.postDeploy(t, rel.ID, delta.ID, true); got != http.StatusUnprocessableEntity {
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
	if got := hc.postDeploy(t, v2.ID, hc.envs["Beta"].ID, false); got != http.StatusUnprocessableEntity {
		t.Errorf("v2 to Beta without v2 on Alpha: got %d, want 422", got)
	}
	// v2 on Alpha is fine.
	if got := hc.postDeploy(t, v2.ID, hc.envs["Alpha"].ID, false); got != http.StatusSeeOther {
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
	if got := hc.postDeploy(t, rel.ID, hc.envs["Alpha"].ID, false); got != http.StatusSeeOther {
		t.Errorf("redeploy to first stage: got %d, want 303", got)
	}
}

func TestReleasesPage_OnlyShowsDeployableEnvsByDefault(t *testing.T) {
	h := newHarness(t)
	hc := h.setupProjectWithLifecycle(t, []string{"Alpha", "Beta"})
	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	resp, err := http.Get(fmt.Sprintf("%s/projects/%d/releases", h.server.URL, hc.project.ID))
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
		t.Errorf("Beta should be in the 'forceable' optgroup; missing data-gate-group")
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
	h.repo.Queries.CreateEnvironment(context.Background(), db.CreateEnvironmentParams{Name: "Outside"})

	rel := hc.makeRelease(t, "1.0.0", "exit 0")

	resp, err := http.Get(fmt.Sprintf("%s/projects/%d/releases", h.server.URL, hc.project.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if strings.Contains(body, `>Outside<`) {
		t.Errorf("non-lifecycle env 'Outside' should NOT appear in the dropdown")
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
func firstDeployment(t *testing.T, h *testHarness, releaseID, envID int64) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		dep, err := h.repo.Queries.GetLatestDeploymentForReleaseEnv(context.Background(), db.GetLatestDeploymentForReleaseEnvParams{
			ReleaseID:     releaseID,
			EnvironmentID: envID,
		})
		if err == nil {
			return dep.ID
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("get latest deployment (release=%d env=%d): %v", releaseID, envID, lastErr)
	return 0
}
