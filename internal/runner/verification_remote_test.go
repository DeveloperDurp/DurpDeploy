package runner_test

import (
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

func startRemoteVerification(t *testing.T) (
	*repository.Repository, *runner.DeploymentRunner, <-chan struct{}, db.Deployment,
) {
	t.Helper()
	// Given: a final step selects one remote Bash agent; verification uses it too.
	repo, rnr, _ := setupRunnerHarness(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	repo.DB.SetMaxOpenConns(1)
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "remote-check"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.CreateEnvironment(
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
	return repo, rnr, done, created.Deployment
}

func TestRemoteVerificationRoutingAndShutdown(t *testing.T) {
	repo, rnr, done, created := startRemoteVerification(t)
	ctx := t.Context()
	for index := int64(0); index < 2; index++ {
		awaitVerificationSignal(t, repo.RemoteWorkReady(), 5*time.Second,
			"remote step was not queued")
		runs, err := repo.Queries.ListRemoteStepRuns(
			ctx,
			db.ListRemoteStepRunsParams{
				DeploymentID: created.ID,
				StepIndex:    index,
			},
		)
		if err != nil || len(runs) != 1 || runs[0].AgentID != "verification-a" {
			t.Fatalf("routing=%+v err=%v", runs, err)
		}
		state := "succeeded"
		if index == 1 {
			state = "claimed"
			assertRemoteVerificationPayload(t, repo)
		}
		if _, err := repo.DB.ExecContext(
			ctx,
			"UPDATE remote_step_runs SET state=? WHERE deployment_id=? AND step_index=?",
			state,
			created.ID,
			index,
		); err != nil {
			t.Fatal(err)
		}
	}
	check, err := repo.Queries.GetDeploymentVerification(
		ctx,
		created.ID,
	)
	if err != nil || check.Status != "running" {
		t.Fatalf("verification=%+v err=%v", check, err)
	}
	// When: shutdown cancels the remote check while awaiting agent acknowledgement.
	shutdown := make(chan struct{})
	go func() { rnr.KillAll(); close(shutdown) }()
	awaitVerificationSignal(t, shutdown, time.Second,
		"shutdown waited for remote confirmation")
	// Then: local shutdown drains promptly, and the remote run still needs confirmation.
	if _, err := repo.DB.ExecContext(
		ctx,
		"UPDATE remote_step_runs SET state='cancelled' WHERE deployment_id=? AND step_index=1",
		created.ID,
	); err != nil {
		t.Fatal(err)
	}
	awaitVerificationSignal(t, done, 5*time.Second,
		"remote verification did not finish after confirmation")
	deployment, err := repo.Queries.GetDeployment(ctx, created.ID)
	if err != nil || deployment.Status != "cancelled" {
		t.Fatalf("deployment=%+v err=%v", deployment, err)
	}
	check, err = repo.Queries.GetDeploymentVerification(
		ctx,
		created.ID,
	)
	if err != nil || check.Status != "cancelled" {
		t.Fatalf("verification=%+v err=%v", check, err)
	}
}

func assertRemoteVerificationPayload(
	t *testing.T,
	repo *repository.Repository,
) {
	t.Helper()
	_, claimed, err := repo.ClaimRemoteStepPayload(
		t.Context(),
		"verification-a",
		func(snapshot repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
			if len(snapshot.Steps) != 1 ||
				snapshot.Steps[0].ScriptBody != "echo remote-check" {
				t.Fatal(
					"agent payload did not recover the encrypted verification script",
				)
			}
			return repository.RemotePreparedClaim{
				Token: "verification-claim", TokenHash: make([]byte, 32),
				Ciphertext: []byte("test-encrypted-payload"),
			}, nil
		},
	)
	if err != nil || !claimed {
		t.Fatalf("verification claim failed: %v", err)
	}
}

func awaitVerificationSignal(
	t *testing.T, signal <-chan struct{}, timeout time.Duration, message string,
) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(timeout):
		t.Fatal(message)
	}
}
