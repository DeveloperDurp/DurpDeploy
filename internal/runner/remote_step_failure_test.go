package runner_test

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunnerPreservesOriginalFailureWhileCancellingSiblings(t *testing.T) {
	ctx := t.Context()
	repo, rnr, recorder := setupRunnerHarness(t)
	repo.DB.SetMaxOpenConns(1)
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "e"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, agentID := range []string{"a", "b"} {
		fingerprint := strings.Repeat(agentID, 64)
		if _, err := repo.DB.ExecContext(ctx, `INSERT INTO agents(
			id,name,endpoint,status,certificate_pem,certificate_fingerprint,
			encrypted_identity)
			VALUES(?,?,?,'active','certificate',?,'identity')`,
			agentID, agentID, "https://agent.invalid", fingerprint,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DB.ExecContext(ctx, `INSERT INTO agent_pairings(
			agent_id,pairing_code_hash,agent_public_identity,agent_pin,
			server_public_identity,server_pin,encrypted_identity,state,
			expires_at,paired_at)
			VALUES(?,?,'public',?,'server-public',?,'identity','paired',
			9999999999,1)`, agentID, bytes.Repeat([]byte(agentID), 32),
			fingerprint, fingerprint,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DB.ExecContext(
			ctx,
			"INSERT INTO agent_labels(agent_id,label) VALUES(?,'linux')",
			agentID,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DB.ExecContext(ctx,
			`INSERT INTO agent_environment_labels(agent_id,environment_id)
			 VALUES(?,?)`, agentID, environment.ID,
		); err != nil {
			t.Fatal(err)
		}
		addAgentInterpreter(t, repo, agentID, "bash")
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "v1",
		StepsJson: `[{
			"name":"remote",
			"script_body":"echo remote",
			"execution_target":"agent",
			"agent_selectors":["linux"],
			"timeout_seconds":10
		}]`,
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
	go func() {
		defer close(done)
		rnr.Run(ctx, created.Deployment.ID, release.ID, environment.ID)
	}()
	select {
	case <-repo.RemoteWorkReady():
	case <-time.After(5 * time.Second):
		t.Fatal("remote step was not queued")
	}
	hashes := map[string][32]byte{
		"a": sha256.Sum256([]byte("claim-a")),
		"b": sha256.Sum256([]byte("claim-b")),
	}
	for _, agentID := range []string{"a", "b"} {
		hash := hashes[agentID]
		if _, err := repo.DB.ExecContext(ctx, `UPDATE remote_step_runs SET
			state='started', claim_token_hash=?, ciphertext='payload',
			claim_expires_at=unixepoch()+60, last_heartbeat_at=unixepoch(),
			started_at=unixepoch() WHERE deployment_id=? AND agent_id=?`,
			hash[:], created.Deployment.ID, agentID,
		); err != nil {
			t.Fatal(err)
		}
	}
	bHash := hashes["b"]
	if handled, err := repo.FinishRemoteStep(
		ctx,
		repository.RemoteLifecycleClaim{
			DeploymentID: created.Deployment.ID,
			AgentID:      "b", ClaimTokenHash: bHash[:],
		},
		"failed",
	); err != nil || !handled {
		t.Fatalf("finish failing agent handled=%v error=%v", handled, err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		runs, err := repo.Queries.ListRemoteStepRuns(ctx,
			db.ListRemoteStepRunsParams{
				DeploymentID: created.Deployment.ID,
				StepIndex:    0,
			})
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 2 && runs[0].State == "cancel_requested" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("sibling cancellation was not requested")
		case <-ticker.C:
		}
	}
	aHash := hashes["a"]
	if changed, handled, err := repo.AcknowledgeRemoteStepCancellation(
		ctx,
		repository.RemoteLifecycleClaim{
			DeploymentID: created.Deployment.ID,
			AgentID:      "a", ClaimTokenHash: aHash[:],
		},
	); err != nil || !changed || !handled {
		t.Fatalf(
			"acknowledge sibling changed=%v handled=%v error=%v",
			changed,
			handled,
			err,
		)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deployment did not finish after sibling cancellation")
	}
	if len(recorder.events) == 0 {
		t.Fatal("deployment failure event was not published")
	}
	message := recorder.events[len(recorder.events)-1].Message
	if !strings.Contains(message, "failed on agent b") {
		t.Fatalf("failure message=%q", message)
	}
}
