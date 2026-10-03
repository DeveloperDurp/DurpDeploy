package runner_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/verification"
)

func startRemoteVerification(t *testing.T, legacy bool) (
	*repository.Repository, *runner.DeploymentRunner, <-chan struct{}, db.Deployment,
) {
	t.Helper()
	// Given: deployment selects an agent; private verification stays on the server.
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
	steps, err := repo.Queries.ListDeploymentSteps(ctx, created.Deployment.ID)
	if err != nil || len(steps) != 2 ||
		steps[1].ExecutionTarget != "local" ||
		steps[1].ContainerImage != verification.BashImage {
		t.Fatalf("verification snapshot=%+v err=%v", steps, err)
	}
	if legacy {
		if _, err := repo.DB.ExecContext(ctx,
			`UPDATE deployment_steps SET execution_target='agent',
			container_image='attacker.invalid/image:latest'
			WHERE deployment_id=? AND step_index=1`, created.Deployment.ID,
		); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); rnr.Run(ctx, created.Deployment.ID, release.ID, environment.ID) }()
	return repo, rnr, done, created.Deployment
}

func TestRemoteVerificationUsesTrustedLocalRuntime(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			checkRemoteVerificationIsolation(t, legacy)
		})
	}
}

func checkRemoteVerificationIsolation(t *testing.T, legacy bool) {
	t.Helper()
	repo, _, done, created := startRemoteVerification(t, legacy)
	ctx := t.Context()
	awaitVerificationSignal(t, repo.RemoteWorkReady(), 5*time.Second,
		"remote step was not queued")
	runs, err := repo.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{
			DeploymentID: created.ID,
			StepIndex:    0,
		},
	)
	if err != nil || len(runs) != 1 || runs[0].AgentID != "verification-a" {
		t.Fatalf("routing=%+v err=%v", runs, err)
	}
	assertRemoteVerificationPayload(t, repo)
	if _, err := repo.DB.ExecContext(
		ctx,
		"UPDATE remote_step_runs SET state=? WHERE deployment_id=? AND step_index=?",
		"succeeded",
		created.ID,
		0,
	); err != nil {
		t.Fatal(err)
	}
	// Then: verification completes locally without sending its script to the agent.
	awaitVerificationSignal(t, done, 5*time.Second,
		"local verification did not finish")
	deployment, err := repo.Queries.GetDeployment(ctx, created.ID)
	if err != nil || deployment.Status != "succeeded" ||
		!deployment.ContainerNamespace.Valid {
		t.Fatalf("deployment=%+v err=%v", deployment, err)
	}
	check, err := repo.Queries.GetDeploymentVerification(
		ctx,
		created.ID,
	)
	if err != nil || check.Status != "succeeded" {
		t.Fatalf("verification=%+v err=%v", check, err)
	}
	runs, err = repo.Queries.ListRemoteStepRuns(ctx,
		db.ListRemoteStepRunsParams{DeploymentID: created.ID, StepIndex: 1})
	if err != nil || len(runs) != 0 {
		t.Fatalf("private verification queued remotely: %+v %v", runs, err)
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
				snapshot.Steps[0].ScriptBody != "echo deploy" ||
				snapshot.Environment.VerificationTarget == "echo remote-check" {
				t.Fatal(
					"agent payload exposed the private verification script",
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
