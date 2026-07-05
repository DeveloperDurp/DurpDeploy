package scheduler_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/scheduler"
)

// testFixture holds a real SQLite-backed repo, a real runner, and a scheduler
// with an injectable clock. The runner is replaced by a capture function so
// tests never spawn real bash processes.
type testFixture struct {
	t      *testing.T
	repo   *repository.Repository
	runner *runner.DeploymentRunner
	sched  *scheduler.Scheduler
	now    time.Time
	logBuf *bytes.Buffer
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()

	// ponytail: file-backed SQLite, not :memory:, because the runner spawns
	// goroutines that need their own DB connection.
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

	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	sched := scheduler.New(repo, rnr,
		scheduler.WithNow(func() time.Time { return now }),
		scheduler.WithLogger(logger),
		scheduler.WithInterval(10*time.Millisecond),
	)

	return &testFixture{
		t:      t,
		repo:   repo,
		runner: rnr,
		sched:  sched,
		now:    now,
		logBuf: logBuf,
	}
}

func (f *testFixture) ctx() context.Context {
	return context.Background()
}

func (f *testFixture) createProject() db.Project {
	f.t.Helper()
	p, err := f.repo.Queries.CreateProject(f.ctx(), db.CreateProjectParams{
		Name:        "test-proj",
		Description: sql.NullString{},
	})
	if err != nil {
		f.t.Fatalf("create project: %v", err)
	}
	return p
}

func (f *testFixture) createRelease(projectID int64) db.Release {
	f.t.Helper()
	r, err := f.repo.Queries.CreateRelease(f.ctx(), db.CreateReleaseParams{
		ProjectID: projectID,
		Version:   "1.0.0",
		StepsJson: "[]",
	})
	if err != nil {
		f.t.Fatalf("create release: %v", err)
	}
	return r
}

func (f *testFixture) createEnvironment(name string) db.Environment {
	f.t.Helper()
	e, err := f.repo.Queries.CreateEnvironment(f.ctx(), db.CreateEnvironmentParams{
		Name:        name,
		Description: sql.NullString{},
		Tags:        sql.NullString{},
	})
	if err != nil {
		f.t.Fatalf("create environment: %v", err)
	}
	return e
}

func (f *testFixture) createLifecycle(name string) db.Lifecycle {
	f.t.Helper()
	lc, err := f.repo.Queries.CreateLifecycle(f.ctx(), db.CreateLifecycleParams{
		Name:        name,
		Description: sql.NullString{},
	})
	if err != nil {
		f.t.Fatalf("create lifecycle: %v", err)
	}
	return lc
}

func (f *testFixture) attachLifecycle(projectID, lifecycleID int64) {
	f.t.Helper()
	err := f.repo.Queries.SetProjectLifecycle(f.ctx(), db.SetProjectLifecycleParams{
		ID:          projectID,
		LifecycleID: sql.NullInt64{Int64: lifecycleID, Valid: true},
	})
	if err != nil {
		f.t.Fatalf("attach lifecycle: %v", err)
	}
}

func (f *testFixture) addLifecycleStage(lifecycleID, envID int64, sortOrder int64) {
	f.t.Helper()
	_, err := f.repo.Queries.CreateLifecycleStage(f.ctx(), db.CreateLifecycleStageParams{
		LifecycleID:   lifecycleID,
		EnvironmentID: envID,
		SortOrder:     sortOrder,
	})
	if err != nil {
		f.t.Fatalf("create lifecycle stage: %v", err)
	}
}

func (f *testFixture) createSchedule(projectID, releaseID, envID int64, cronExpr string, nextRunAt time.Time, enabled int64, note string) db.ScheduledDeployment {
	f.t.Helper()
	s, err := f.repo.Queries.CreateScheduledDeployment(f.ctx(), db.CreateScheduledDeploymentParams{
		ProjectID:     projectID,
		ReleaseID:     releaseID,
		EnvironmentID: envID,
		Cron:          cronExpr,
		NextRunAt:     nextRunAt.Unix(),
		Enabled:       enabled,
		LastFiredAt:   sql.NullInt64{},
		Note:          sql.NullString{String: note, Valid: true},
	})
	if err != nil {
		f.t.Fatalf("create scheduled deployment: %v", err)
	}
	return s
}

func (f *testFixture) createDeployment(releaseID, envID int64, status string) db.Deployment {
	f.t.Helper()
	d, err := f.repo.Queries.CreateDeployment(f.ctx(), db.CreateDeploymentParams{
		ReleaseID:     releaseID,
		EnvironmentID: envID,
		Status:        status,
		StartedAt:     sql.NullInt64{},
		FinishedAt:    sql.NullInt64{},
		Forced:        0,
		Note:          sql.NullString{},
	})
	if err != nil {
		f.t.Fatalf("create deployment: %v", err)
	}
	return d
}

func (f *testFixture) advanceNow(d time.Duration) {
	f.now = f.now.Add(d)
}

func (f *testFixture) captureRunCalls() *[]struct{ DeploymentID, ReleaseID, EnvironmentID int64 } {
	f.t.Helper()
	var mu sync.Mutex
	calls := []struct{ DeploymentID, ReleaseID, EnvironmentID int64 }{}
	f.sched.SetRunFunc(func(ctx context.Context, deploymentID, releaseID, environmentID int64) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, struct{ DeploymentID, ReleaseID, EnvironmentID int64 }{deploymentID, releaseID, environmentID})
	})
	return &calls
}

// --- tests ---

func TestTick_DueRow_FiresAndAdvances(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	rel := f.createRelease(proj.ID)
	env := f.createEnvironment("dev")
	sched := f.createSchedule(proj.ID, rel.ID, env.ID, "* * * * *", f.now.Add(-time.Minute), 1, "nightly")

	calls := f.captureRunCalls()

	f.sched.Tick(f.ctx())

	// assert runner was called
	if len(*calls) != 1 {
		t.Fatalf("expected 1 run call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.ReleaseID != rel.ID || c.EnvironmentID != env.ID {
		t.Fatalf("runner called with wrong args: %+v", c)
	}

	// assert deployment created with scheduled note
	deps, err := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), rel.ID)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 deployment, got %d", len(deps))
	}
	if deps[0].Status != "pending" {
		t.Fatalf("expected status pending, got %s", deps[0].Status)
	}
	wantNote := fmt.Sprintf("Scheduled: %d - nightly", sched.ID)
	if deps[0].Note.String != wantNote {
		t.Fatalf("expected note %q, got %q", wantNote, deps[0].Note.String)
	}

	// assert next_run_at advanced to a future time
	updated, err := f.repo.Queries.GetScheduledDeployment(f.ctx(), sched.ID)
	if err != nil {
		t.Fatalf("get scheduled: %v", err)
	}
	if updated.NextRunAt <= f.now.Unix() {
		t.Fatalf("next_run_at not advanced: %d <= %d", updated.NextRunAt, f.now.Unix())
	}

	// assert log contains fired action
	logs := f.logBuf.String()
	if !strings.Contains(logs, "fired") {
		t.Fatalf("log missing 'fired': %s", logs)
	}
	if !strings.Contains(logs, fmt.Sprintf("%d", sched.ID)) {
		t.Fatalf("log missing schedule id: %s", logs)
	}
}

func TestTick_BadCron_ParksAndLogs(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	rel := f.createRelease(proj.ID)
	env := f.createEnvironment("dev")
	sched := f.createSchedule(proj.ID, rel.ID, env.ID, "bogus", f.now.Add(-time.Minute), 1, "bad")

	f.sched.Tick(f.ctx())

	// assert no deployment created
	deps, _ := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), rel.ID)
	if len(deps) != 0 {
		t.Fatalf("expected 0 deployments, got %d", len(deps))
	}

	// assert parked (now+10y)
	updated, _ := f.repo.Queries.GetScheduledDeployment(f.ctx(), sched.ID)
	wantParked := f.now.Add(10 * 365 * 24 * time.Hour).Unix()
	if updated.NextRunAt < wantParked-60 || updated.NextRunAt > wantParked+60 {
		t.Fatalf("expected next_run_at near %d, got %d", wantParked, updated.NextRunAt)
	}

	logs := f.logBuf.String()
	if !strings.Contains(logs, "invalid cron") {
		t.Fatalf("log missing 'invalid cron': %s", logs)
	}
	if !strings.Contains(logs, "parked") {
		t.Fatalf("log missing 'parked': %s", logs)
	}
}

func TestTick_UnsatisfiableCron_ParksAndLogs(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	rel := f.createRelease(proj.ID)
	env := f.createEnvironment("dev")
	// Feb 30 never happens
	sched := f.createSchedule(proj.ID, rel.ID, env.ID, "0 0 30 2 *", f.now.Add(-time.Minute), 1, "impossible")

	f.sched.Tick(f.ctx())

	deps, _ := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), rel.ID)
	if len(deps) != 0 {
		t.Fatalf("expected 0 deployments, got %d", len(deps))
	}

	updated, _ := f.repo.Queries.GetScheduledDeployment(f.ctx(), sched.ID)
	wantParked := f.now.Add(10 * 365 * 24 * time.Hour).Unix()
	if updated.NextRunAt < wantParked-60 || updated.NextRunAt > wantParked+60 {
		t.Fatalf("expected next_run_at near %d, got %d", wantParked, updated.NextRunAt)
	}

	logs := f.logBuf.String()
	if !strings.Contains(logs, "invalid cron: unsatisfiable") {
		t.Fatalf("log missing 'invalid cron: unsatisfiable': %s", logs)
	}
	if !strings.Contains(logs, "parked") {
		t.Fatalf("log missing 'parked': %s", logs)
	}
}

func TestTick_Overlap_SkipsAndAdvances(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	rel := f.createRelease(proj.ID)
	env := f.createEnvironment("dev")
	// existing running deployment for same release+env
	f.createDeployment(rel.ID, env.ID, "running")
	sched := f.createSchedule(proj.ID, rel.ID, env.ID, "* * * * *", f.now.Add(-time.Minute), 1, "overlap")

	calls := f.captureRunCalls()
	f.sched.Tick(f.ctx())

	if len(*calls) != 0 {
		t.Fatalf("expected 0 run calls, got %d", len(*calls))
	}

	// assert no new deployment
	deps, _ := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), rel.ID)
	if len(deps) != 1 {
		t.Fatalf("expected 1 deployment (the existing one), got %d", len(deps))
	}

	// assert next_run_at advanced
	updated, _ := f.repo.Queries.GetScheduledDeployment(f.ctx(), sched.ID)
	if updated.NextRunAt <= f.now.Unix() {
		t.Fatalf("next_run_at not advanced: %d <= %d", updated.NextRunAt, f.now.Unix())
	}

	logs := f.logBuf.String()
	if !strings.Contains(logs, "skipped_overlap") {
		t.Fatalf("log missing 'skipped_overlap': %s", logs)
	}
}

func TestTick_GateBlock_SkipsAndAdvances(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	lc := f.createLifecycle("prod-lc")
	f.attachLifecycle(proj.ID, lc.ID)

	dev := f.createEnvironment("dev")
	prod := f.createEnvironment("prod")
	f.addLifecycleStage(lc.ID, dev.ID, 0)
	f.addLifecycleStage(lc.ID, prod.ID, 1)

	rel := f.createRelease(proj.ID)
	// no successful deployment to dev, so prod is gated
	sched := f.createSchedule(proj.ID, rel.ID, prod.ID, "* * * * *", f.now.Add(-time.Minute), 1, "gate")

	calls := f.captureRunCalls()
	f.sched.Tick(f.ctx())

	if len(*calls) != 0 {
		t.Fatalf("expected 0 run calls, got %d", len(*calls))
	}

	deps, _ := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), rel.ID)
	if len(deps) != 0 {
		t.Fatalf("expected 0 deployments, got %d", len(deps))
	}

	updated, _ := f.repo.Queries.GetScheduledDeployment(f.ctx(), sched.ID)
	if updated.NextRunAt <= f.now.Unix() {
		t.Fatalf("next_run_at not advanced: %d <= %d", updated.NextRunAt, f.now.Unix())
	}

	logs := f.logBuf.String()
	if !strings.Contains(logs, "skipped_gate") {
		t.Fatalf("log missing 'skipped_gate': %s", logs)
	}
}

func TestTick_Disabled_NotInDueList(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	rel := f.createRelease(proj.ID)
	env := f.createEnvironment("dev")
	sched := f.createSchedule(proj.ID, rel.ID, env.ID, "* * * * *", f.now.Add(-time.Minute), 0, "disabled")

	calls := f.captureRunCalls()
	f.sched.Tick(f.ctx())

	if len(*calls) != 0 {
		t.Fatalf("expected 0 run calls, got %d", len(*calls))
	}

	updated, _ := f.repo.Queries.GetScheduledDeployment(f.ctx(), sched.ID)
	if updated.NextRunAt != sched.NextRunAt {
		t.Fatalf("next_run_at changed for disabled schedule: %d != %d", updated.NextRunAt, sched.NextRunAt)
	}
}

func TestTick_NoDueRows_Noop(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	rel := f.createRelease(proj.ID)
	env := f.createEnvironment("dev")
	// schedule in the future, not due
	f.createSchedule(proj.ID, rel.ID, env.ID, "* * * * *", f.now.Add(time.Hour), 1, "future")

	calls := f.captureRunCalls()
	f.sched.Tick(f.ctx())

	if len(*calls) != 0 {
		t.Fatalf("expected 0 run calls, got %d", len(*calls))
	}
}

func TestStop_GoroutineExitsCleanly(t *testing.T) {
	f := newFixture(t)

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	f.sched.Start(ctx)

	// let it start
	time.Sleep(50 * time.Millisecond)

	cancel()
	f.sched.Stop()

	// let goroutines settle
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	// allow a small margin for test-runner goroutines
	if after > before+2 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestTick_GatePass_Fires(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	lc := f.createLifecycle("prod-lc")
	f.attachLifecycle(proj.ID, lc.ID)

	dev := f.createEnvironment("dev")
	prod := f.createEnvironment("prod")
	f.addLifecycleStage(lc.ID, dev.ID, 0)
	f.addLifecycleStage(lc.ID, prod.ID, 1)

	rel := f.createRelease(proj.ID)
	// successful deployment to dev unlocks prod
	f.createDeployment(rel.ID, dev.ID, "succeeded")
	_ = f.createSchedule(proj.ID, rel.ID, prod.ID, "* * * * *", f.now.Add(-time.Minute), 1, "gate-pass")

	calls := f.captureRunCalls()
	f.sched.Tick(f.ctx())

	if len(*calls) != 1 {
		t.Fatalf("expected 1 run call, got %d", len(*calls))
	}

	deps, _ := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), rel.ID)
	if len(deps) != 2 {
		t.Fatalf("expected 2 deployments (dev + scheduled), got %d", len(deps))
	}
}

func TestTick_ParseError_ParksAndLogs(t *testing.T) {
	f := newFixture(t)
	proj := f.createProject()
	rel := f.createRelease(proj.ID)
	env := f.createEnvironment("dev")
	// 6-field cron (with seconds) is rejected by the 5-field parser
	sched := f.createSchedule(proj.ID, rel.ID, env.ID, "0 * * * * *", f.now.Add(-time.Minute), 1, "6field")

	f.sched.Tick(f.ctx())

	deps, _ := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), rel.ID)
	if len(deps) != 0 {
		t.Fatalf("expected 0 deployments, got %d", len(deps))
	}

	updated, _ := f.repo.Queries.GetScheduledDeployment(f.ctx(), sched.ID)
	wantParked := f.now.Add(10 * 365 * 24 * time.Hour).Unix()
	if updated.NextRunAt < wantParked-60 || updated.NextRunAt > wantParked+60 {
		t.Fatalf("expected next_run_at near %d, got %d", wantParked, updated.NextRunAt)
	}

	logs := f.logBuf.String()
	if !strings.Contains(logs, "invalid cron") {
		t.Fatalf("log missing 'invalid cron': %s", logs)
	}
	if !strings.Contains(logs, "parked") {
		t.Fatalf("log missing 'parked': %s", logs)
	}
}
