package runner_test

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestCancelDoesNotFinalizeDeploymentBeforeRunnerStops(t *testing.T) {
	repo, deploymentRunner, _ := setupRunnerHarness(t)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "cancel"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "cancel"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := false
	deploymentRunner.RegisterCancel(deployment.ID, func() { cancelled = true })

	if err := deploymentRunner.Cancel(deployment.ID); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("registered runner cancellation was not requested")
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "running" || stored.FinishedAt.Valid {
		t.Fatalf("deployment finalized before runner stopped: %+v", stored)
	}
}

func TestControllerCancelWaitsForRemoteAcknowledgement(t *testing.T) {
	fixture := startRemoteCancellationRun(t)
	if err := fixture.runner.Cancel(fixture.deploymentID); err != nil {
		t.Fatal(err)
	}
	fixture.waitForRunState(t, "cancel_requested")
	deployment, err := fixture.repo.Queries.GetDeployment(
		t.Context(), fixture.deploymentID,
	)
	if err != nil || deployment.Status != "running" {
		t.Fatalf(
			"deployment before acknowledgement=%+v error=%v",
			deployment,
			err,
		)
	}
	select {
	case <-fixture.done:
		t.Fatal("runner stopped before cancellation acknowledgement")
	default:
	}
	changed, handled, err := fixture.repo.AcknowledgeRemoteStepCancellation(
		t.Context(),
		repository.RemoteLifecycleClaim{
			DeploymentID:   fixture.deploymentID,
			AgentID:        "agent",
			ClaimTokenHash: fixture.claimHash[:],
		},
	)
	if err != nil || !changed || !handled {
		t.Fatalf(
			"acknowledge changed=%v handled=%v error=%v",
			changed,
			handled,
			err,
		)
	}
	fixture.waitForRunner(t)
	deployment, err = fixture.repo.Queries.GetDeployment(
		t.Context(), fixture.deploymentID,
	)
	if err != nil || deployment.Status != "cancelled" ||
		!deployment.FinishedAt.Valid {
		t.Fatalf(
			"deployment after acknowledgement=%+v error=%v",
			deployment,
			err,
		)
	}
}

func TestControllerCancelDoesNotMaskCancelUnconfirmed(t *testing.T) {
	fixture := startRemoteCancellationRun(t)
	if err := fixture.runner.Cancel(fixture.deploymentID); err != nil {
		t.Fatal(err)
	}
	fixture.waitForRunState(t, "cancel_requested")
	if _, err := fixture.repo.DB.ExecContext(
		t.Context(),
		`UPDATE remote_step_runs SET state='cancel_unconfirmed',
		 finished_at=unixepoch(), updated_at=unixepoch()
		 WHERE deployment_id=? AND agent_id='agent'`,
		fixture.deploymentID,
	); err != nil {
		t.Fatal(err)
	}
	fixture.waitForRunner(t)
	deployment, err := fixture.repo.Queries.GetDeployment(
		t.Context(), fixture.deploymentID,
	)
	if err != nil || deployment.Status != "failed" {
		t.Fatalf(
			"unconfirmed cancellation deployment=%+v error=%v",
			deployment,
			err,
		)
	}
}
