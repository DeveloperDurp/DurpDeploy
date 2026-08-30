package runner_test

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

type recordingNotifier struct {
	events []events.Event
}

func (r *recordingNotifier) Name() string { return "recording" }

func (r *recordingNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	r.events = append(r.events, event)
	return false, nil
}

func setupRunnerHarness(
	t *testing.T,
) (*repository.Repository, *runner.DeploymentRunner, *recordingNotifier) {
	t.Helper()
	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })

	repo := repository.New(dbConn)
	rec := &recordingNotifier{}
	bus := events.NewBus(repo)
	bus.Register(rec)

	rnr := runner.New(repo, runner.NewLogBroker())
	rnr.SetEventBus(bus)
	return repo, rnr, rec
}

// TestRunner_PublishesStartedAndSucceededEvents: a deployment whose steps
// all succeed publishes deployment_started then deployment_succeeded, in
// that order.
func TestRunner_PublishesStartedAndSucceededEvents(t *testing.T) {
	ctx := context.Background()
	repo, rnr, rec := setupRunnerHarness(t)

	proj, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p1"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "prod"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: proj.ID,
		Version:   "v1",
		StepsJson: `[]`,
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	dep, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "pending",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	rnr.Run(ctx, dep.ID, release.ID, env.ID)

	if len(rec.events) != 2 {
		t.Fatalf(
			"published events = %d, want 2 (started, succeeded); got %+v",
			len(rec.events),
			rec.events,
		)
	}
	if rec.events[0].Type != events.DeploymentStarted {
		t.Fatalf(
			"first event type = %q, want %q",
			rec.events[0].Type,
			events.DeploymentStarted,
		)
	}
	if rec.events[1].Type != events.DeploymentSucceeded {
		t.Fatalf(
			"second event type = %q, want %q",
			rec.events[1].Type,
			events.DeploymentSucceeded,
		)
	}
	for _, e := range rec.events {
		if e.ProjectID != proj.ID {
			t.Fatalf("event project id = %d, want %d", e.ProjectID, proj.ID)
		}
		if e.DeploymentID != dep.ID {
			t.Fatalf(
				"event deployment id = %d, want %d",
				e.DeploymentID,
				dep.ID,
			)
		}
	}

	rows, err := repo.Queries.ListNotificationEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list notification events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("notification_events rows = %d, want 2", len(rows))
	}
}

// TestRunner_PublishesFailedEvent: a deployment whose step fails publishes
// deployment_started then deployment_failed.
func TestRunner_PublishesFailedEvent(t *testing.T) {
	ctx := context.Background()
	repo, rnr, rec := setupRunnerHarness(t)

	proj, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p1"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "prod"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: proj.ID,
		Version:   "v1",
		StepsJson: `[{"name":"step1","script_body":"exit 1","sort_order":1,"timeout_seconds":5,"max_retries":0}]`,
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	dep, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "pending",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	rnr.Run(ctx, dep.ID, release.ID, env.ID)

	if len(rec.events) != 2 {
		t.Fatalf(
			"published events = %d, want 2 (started, failed); got %+v",
			len(rec.events),
			rec.events,
		)
	}
	if rec.events[1].Type != events.DeploymentFailed {
		t.Fatalf(
			"second event type = %q, want %q",
			rec.events[1].Type,
			events.DeploymentFailed,
		)
	}
}
