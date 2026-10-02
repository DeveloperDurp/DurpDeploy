package runner_test

import (
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestRemoteVerificationRoutingAndShutdown(t *testing.T) {
	// Given: a final step selects one remote Bash agent; verification uses it too.
	repo, rnr, _ := setupRunnerHarness(t)
	repo.DB.SetMaxOpenConns(1)
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "remote-check"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{
			Name:                       "production",
			VerificationType:           "bash",
			VerificationTarget:         "echo remote-check",
			VerificationTimeoutSeconds: 10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	seedAgentWithInterpreter(t, repo, environment.ID, "verification-a", "bash")
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "v1",
		StepsJson: `[{"name":"deploy","script_body":"echo deploy","execution_target":"agent","agent_selectors":["linux"],"interpreter":"bash","timeout_seconds":10}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); rnr.Run(ctx, created.Deployment.ID, release.ID, environment.ID) }()
	for index := int64(0); index < 2; index++ {
		select {
		case <-repo.RemoteWorkReady():
		case <-time.After(5 * time.Second):
			t.Fatal("remote step was not queued")
		}
		runs, err := repo.Queries.ListRemoteStepRuns(
			ctx,
			db.ListRemoteStepRunsParams{
				DeploymentID: created.Deployment.ID,
				StepIndex:    index,
			},
		)
		if err != nil || len(runs) != 1 || runs[0].AgentID != "verification-a" {
			t.Fatalf("routing=%+v err=%v", runs, err)
		}
		state := "succeeded"
		if index == 1 {
			state = "claimed"
		}
		if _, err := repo.DB.ExecContext(
			ctx,
			"UPDATE remote_step_runs SET state=? WHERE deployment_id=? AND step_index=?",
			state,
			created.Deployment.ID,
			index,
		); err != nil {
			t.Fatal(err)
		}
	}
	check, err := repo.Queries.GetDeploymentVerification(
		ctx,
		created.Deployment.ID,
	)
	if err != nil || check.Status != "running" {
		t.Fatalf("verification=%+v err=%v", check, err)
	}
	// When: shutdown cancels the remote check while awaiting agent acknowledgement.
	shutdown := make(chan struct{})
	go func() { rnr.KillAll(); close(shutdown) }()
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for remote confirmation")
	}
	// Then: local shutdown drains promptly, and the remote run still needs confirmation.
	if _, err := repo.DB.ExecContext(
		ctx,
		"UPDATE remote_step_runs SET state='cancelled' WHERE deployment_id=? AND step_index=1",
		created.Deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("remote verification did not finish after confirmation")
	}
	deployment, err := repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || deployment.Status != "cancelled" {
		t.Fatalf("deployment=%+v err=%v", deployment, err)
	}
	check, err = repo.Queries.GetDeploymentVerification(
		ctx,
		created.Deployment.ID,
	)
	if err != nil || check.Status != "cancelled" {
		t.Fatalf("verification=%+v err=%v", check, err)
	}
}
