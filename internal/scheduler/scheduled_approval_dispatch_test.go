package scheduler_test

import (
	"testing"
	"time"

	"durpdeploy/internal/dispatch"
)

func TestScheduledApproval_DispatchFrozenWaitsForApproval(t *testing.T) {
	// Given an occurrence whose authoritative creation requires approval.
	f := newFixture(t)
	project := f.createProject()
	release := f.createRelease(project.ID)
	environment := f.createEnvironment("approval")
	lifecycle := f.createLifecycle("approval")
	f.attachLifecycle(project.ID, lifecycle.ID)
	f.addLifecycleStageWithApproval(lifecycle.ID, environment.ID, 0)
	schedule := f.createSchedule(
		project.ID, release.ID, environment.ID, "* * * * *",
		f.now.Add(-time.Minute), 1, "approval",
	)
	creator := dispatch.NewCreationService(
		f.repo, dispatch.New(f.repo, nil, nil),
	)
	deployment, err := creator.CreateScheduled(
		f.ctx(), dispatch.ScheduledRequest{
			CreateRequest: dispatch.CreateRequest{
				ProjectID: project.ID, ReleaseID: release.ID,
				EnvironmentID: environment.ID,
			},
			ScheduleID: schedule.ID, DueAt: schedule.NextRunAt,
			NextRunAt: f.now.Add(time.Minute).Unix(),
		},
	)
	if err != nil || deployment.Status != "pending_approval" {
		t.Fatalf("created status = %q, error = %v", deployment.Status, err)
	}

	// When a stale scheduler decision or recovery repeats dispatch.
	for range 2 {
		if err := creator.DispatchFrozen(f.ctx(), deployment.ID); err != nil {
			t.Fatalf("dispatch pending approval: %v", err)
		}
	}

	// Then no runnable artifacts exist and the approval state is unchanged.
	var runnable int
	if err := f.repo.DB.QueryRowContext(f.ctx(),
		"SELECT COUNT(*) FROM deployment_dispatches WHERE deployment_id = ?",
		deployment.ID,
	).Scan(&runnable); err != nil {
		t.Fatal(err)
	}
	t.Logf("pending_approval dispatch rows = %d", runnable)
	assertNoScheduledRoutingArtifacts(t, f, deployment.ID, 0)
	root, err := f.repo.Queries.GetDeployment(f.ctx(), deployment.ID)
	if err != nil || root.Status != "pending_approval" {
		t.Fatalf("root status = %q, error = %v", root.Status, err)
	}
	t.Log(
		"pending_approval root; zero routing artifacts after two dispatch calls",
	)
}
