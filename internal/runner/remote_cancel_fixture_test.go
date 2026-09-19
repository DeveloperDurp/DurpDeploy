package runner_test

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

type remoteCancellationHarness struct {
	repo         *repository.Repository
	runner       *runner.DeploymentRunner
	deploymentID int64
	claimHash    [32]byte
	done         <-chan struct{}
}

func startRemoteCancellationRun(t *testing.T) remoteCancellationHarness {
	t.Helper()
	repo, deploymentRunner, _ := setupRunnerHarness(t)
	repo.DB.SetMaxOpenConns(1)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "remote-cancel"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "remote-cancel"},
	)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("a", 64)
	if _, err := repo.DB.ExecContext(t.Context(), `INSERT INTO agents(
		id,name,endpoint,status,certificate_pem,certificate_fingerprint,
		encrypted_identity) VALUES(
		'agent','agent','https://agent.invalid','active','certificate',?,'identity')`,
		fingerprint,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), `INSERT INTO agent_pairings(
		agent_id,pairing_code_hash,agent_public_identity,agent_pin,
		server_public_identity,server_pin,encrypted_identity,state,
		expires_at,paired_at) VALUES(
		'agent',?,'public',?,'server-public',?,'identity','paired',9999999999,1)`,
		bytes.Repeat([]byte{1}, 32), fingerprint, fingerprint,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(
		t.Context(),
		"INSERT INTO agent_labels(agent_id,label) VALUES('agent','linux')",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(
		t.Context(),
		`INSERT INTO agent_environment_labels(agent_id,environment_id)
		 VALUES('agent',?)`,
		environment.ID,
	); err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: `[{
				"name":"remote","script_body":"echo remote",
				"execution_target":"agent","agent_selectors":["linux"],
				"timeout_seconds":60
			}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		deploymentRunner.Run(
			t.Context(), created.Deployment.ID, release.ID, environment.ID,
		)
	}()
	select {
	case <-repo.RemoteWorkReady():
	case <-time.After(5 * time.Second):
		t.Fatal("remote step was not queued")
	}
	claimHash := sha256.Sum256([]byte("claim"))
	if _, err := repo.DB.ExecContext(t.Context(), `UPDATE remote_step_runs SET
		state='started', claim_token_hash=?, ciphertext='payload',
		claim_expires_at=unixepoch()+60, last_heartbeat_at=unixepoch(),
		started_at=unixepoch() WHERE deployment_id=? AND agent_id='agent'`,
		claimHash[:], created.Deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	return remoteCancellationHarness{
		repo: repo, runner: deploymentRunner,
		deploymentID: created.Deployment.ID, claimHash: claimHash, done: done,
	}
}

func (fixture remoteCancellationHarness) waitForRunState(
	t *testing.T,
	want string,
) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		runs, err := fixture.repo.Queries.ListRemoteStepRuns(
			t.Context(),
			db.ListRemoteStepRunsParams{
				DeploymentID: fixture.deploymentID, StepIndex: 0,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 1 && runs[0].State == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("remote run did not reach %s: %+v", want, runs)
		case <-ticker.C:
		}
	}
}

func (fixture remoteCancellationHarness) waitForRunner(t *testing.T) {
	t.Helper()
	select {
	case <-fixture.done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop")
	}
}
