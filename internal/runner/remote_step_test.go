package runner_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// seedAgentWithInterpreter inserts an active, paired agent with the given
// label and interpreter capability, matched to the environment.
func seedAgentWithInterpreter(
	t *testing.T,
	repo *repository.Repository,
	environmentID int64,
	agentID, interpreter string,
) {
	t.Helper()
	ctx := t.Context()
	fingerprint := strings.Repeat(agentID[len(agentID)-1:], 64)
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
		9999999999,1)`, agentID,
		bytes.Repeat([]byte(agentID[len(agentID)-1:]), 32),
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
		 VALUES(?,?)`, agentID, environmentID,
	); err != nil {
		t.Fatal(err)
	}
	addAgentInterpreter(t, repo, agentID, interpreter)
}

func TestRunnerMixedLocalAndAgentStepsSucceedInOrder(t *testing.T) {
	ctx := context.Background()
	repo, rnr, _ := setupRunnerHarness(t)
	repo.DB.SetMaxOpenConns(1)
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "mixed-interpreters"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "production"},
	)
	if err != nil {
		t.Fatal(err)
	}
	seedAgentWithInterpreter(t, repo, environment.ID, "agent-a", "bash")
	marker := filepath.Join(t.TempDir(), "order.txt")
	binDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(binDir, "python3"),
		[]byte("#!/bin/sh\necho local-python >> "+marker+"\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	steps, err := json.Marshal([]map[string]any{
		{
			"name": "local", "script_body": "print(1)",
			"interpreter":      "python3",
			"execution_target": "local",
			"sort_order":       1, "timeout_seconds": 5,
		},
		{
			"name": "remote", "script_body": "echo agent-ok",
			"interpreter":      "bash",
			"execution_target": "agent",
			"agent_selectors":  []string{"linux"},
			"sort_order":       2, "timeout_seconds": 10,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: string(steps),
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
		t.Fatal("remote step was not queued after the local step")
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "local-python\n" {
		t.Fatalf(
			"local python3 step did not run before the agent step: %q",
			contents,
		)
	}
	if _, err := repo.DB.ExecContext(ctx, `
UPDATE remote_step_runs SET state='succeeded', started_at=unixepoch(),
finished_at=unixepoch() WHERE deployment_id=? AND step_index=1`,
		created.Deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deployment did not finish after the agent step succeeded")
	}
	deployment, err := repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || deployment.Status != "succeeded" {
		t.Fatalf("deployment = %+v, error = %v", deployment, err)
	}
	runs, err := repo.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{
			DeploymentID: created.Deployment.ID, StepIndex: 1,
		},
	)
	if err != nil || len(runs) != 1 || runs[0].State != "succeeded" {
		t.Fatalf("remote runs = %+v, error = %v", runs, err)
	}
	contents, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "local-python\n" {
		t.Fatalf("local step ran again after the agent step: %q", contents)
	}
}

func TestRunnerWaitsForEveryRemoteAgentBeforeContinuing(t *testing.T) {
	ctx := context.Background()
	repo, rnr, _ := setupRunnerHarness(t)
	repo.DB.SetMaxOpenConns(1)
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "mixed"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "production"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, agentID := range []string{"agent-a", "agent-b"} {
		fingerprint := strings.Repeat(string(agentID[len(agentID)-1]), 64)
		if _, err := repo.DB.ExecContext(ctx, `
INSERT INTO agents(
    id,name,endpoint,status,certificate_pem,certificate_fingerprint,
    encrypted_identity
) VALUES(?,?,?,'active','certificate',?,'identity')`,
			agentID, agentID, "https://agent.invalid", fingerprint,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DB.ExecContext(ctx, `
INSERT INTO agent_pairings(
    agent_id,pairing_code_hash,agent_public_identity,agent_pin,
    server_public_identity,server_pin,encrypted_identity,state,expires_at,paired_at
) VALUES(?,?,'public',?,'server-public',?,'identity',
    'paired',9999999999,1)`,
			agentID, bytes.Repeat([]byte(agentID[len(agentID)-1:]), 32),
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
		if _, err := repo.DB.ExecContext(ctx, `
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES(?,?)`,
			agentID, environment.ID,
		); err != nil {
			t.Fatal(err)
		}
		addAgentInterpreter(t, repo, agentID, "bash")
	}
	marker := filepath.Join(t.TempDir(), "order.txt")
	steps, err := json.Marshal([]map[string]any{
		{
			"name": "before", "script_body": "echo before >> " + marker,
			"execution_target": "local",
		},
		{
			"name": "remote", "script_body": "echo remote",
			"execution_target": "agent", "agent_selectors": []string{"linux"},
			"timeout_seconds": 10,
		},
		{
			"name": "after", "script_body": "echo after >> " + marker,
			"execution_target": "local",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: string(steps),
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
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "before\n" {
		t.Fatalf("local order before remote completion = %q", contents)
	}
	runs, err := repo.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{
			DeploymentID: created.Deployment.ID, StepIndex: 1,
		},
	)
	if err != nil || len(runs) != 2 {
		t.Fatalf("remote runs = %+v, error = %v", runs, err)
	}
	if _, err := repo.DB.ExecContext(ctx, `
UPDATE remote_step_runs SET state='succeeded', started_at=unixepoch(),
finished_at=unixepoch() WHERE deployment_id=? AND step_index=1`,
		created.Deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deployment did not continue after every agent succeeded")
	}
	contents, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "before\nafter\n" {
		t.Fatalf("final local order = %q", contents)
	}
	deployment, err := repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || deployment.Status != "succeeded" ||
		!deployment.FinishedAt.Valid {
		t.Fatalf("deployment = %+v, error = %v", deployment, err)
	}
}

func TestRunnerRequestsCancellationBeforeTimeoutFailure(t *testing.T) {
	ctx := t.Context()
	repo, rnr, _ := setupRunnerHarness(t)
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
		fingerprint := strings.Repeat(agentID[len(agentID)-1:], 64)
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
	steps, err := json.Marshal([]map[string]any{{
		"name": "remote", "script_body": "echo remote",
		"execution_target": "agent", "agent_selectors": []string{"linux"},
		"timeout_seconds": 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: string(steps),
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
	claimHash := sha256.Sum256([]byte("claim"))
	if _, err := repo.DB.ExecContext(ctx, `UPDATE remote_step_runs SET
		state='started', claim_token_hash=?, ciphertext='payload',
		claim_expires_at=unixepoch()+60, last_heartbeat_at=unixepoch(),
		started_at=unixepoch() WHERE deployment_id=? AND step_index=0
		AND agent_id='a'`,
		claimHash[:], created.Deployment.ID,
	); err != nil {
		t.Fatal(err)
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
		if len(runs) == 2 && runs[0].State == "cancel_requested" &&
			runs[1].State == "cancelled" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("timeout did not request remote cancellation")
		case <-ticker.C:
		}
	}
	deployment, err := repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || deployment.Status != "running" {
		t.Fatalf(
			"deployment before cancellation acknowledgement=%+v error=%v",
			deployment,
			err,
		)
	}
	select {
	case <-done:
		t.Fatal("deployment finished while an agent still owned active work")
	case <-time.After(500 * time.Millisecond):
	}
	changed, handled, err := repo.AcknowledgeRemoteStepCancellation(
		ctx,
		repository.RemoteLifecycleClaim{
			DeploymentID:   created.Deployment.ID,
			AgentID:        "a",
			ClaimTokenHash: claimHash[:],
		},
	)
	if err != nil || !changed || !handled {
		t.Fatalf(
			"acknowledge cancellation changed=%v handled=%v error=%v",
			changed,
			handled,
			err,
		)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(
			"deployment did not finish after cancellation acknowledgement",
		)
	}
	runs, err := repo.Queries.ListRemoteStepRuns(ctx,
		db.ListRemoteStepRunsParams{
			DeploymentID: created.Deployment.ID,
			StepIndex:    0,
		})
	if err != nil || len(runs) != 2 || runs[0].State != "cancelled" ||
		runs[1].State != "cancelled" {
		t.Fatalf("remote runs=%+v error=%v", runs, err)
	}
	deployment, err = repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || deployment.Status != "failed" {
		t.Fatalf(
			"deployment after cancellation acknowledgement=%+v error=%v",
			deployment,
			err,
		)
	}
}

// TestRunnerRequestsCancellationBeforeTimeoutFailurePython3 mirrors the
// bash cancellation test with a python3 agent-backed step: the step timeout
// must request remote cancellation instead of failing outright, and the
// deployment only finishes after the agent acknowledges.
func TestRunnerRequestsCancellationBeforeTimeoutFailurePython3(
	t *testing.T,
) {
	ctx := t.Context()
	repo, rnr, _ := setupRunnerHarness(t)
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
	seedAgentWithInterpreter(t, repo, environment.ID, "a", "python3")
	seedAgentWithInterpreter(t, repo, environment.ID, "b", "python3")
	steps, err := json.Marshal([]map[string]any{{
		"name": "remote", "script_body": "print('remote')",
		"interpreter":      "python3",
		"execution_target": "agent", "agent_selectors": []string{"linux"},
		"timeout_seconds": 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: string(steps),
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
	claimHash := sha256.Sum256([]byte("claim"))
	if _, err := repo.DB.ExecContext(ctx, `UPDATE remote_step_runs SET
		state='started', claim_token_hash=?, ciphertext='payload',
		claim_expires_at=unixepoch()+60, last_heartbeat_at=unixepoch(),
		started_at=unixepoch() WHERE deployment_id=? AND step_index=0
		AND agent_id='a'`,
		claimHash[:], created.Deployment.ID,
	); err != nil {
		t.Fatal(err)
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
		if len(runs) == 2 && runs[0].State == "cancel_requested" &&
			runs[1].State == "cancelled" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("timeout did not request remote cancellation")
		case <-ticker.C:
		}
	}
	deployment, err := repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || deployment.Status != "running" {
		t.Fatalf(
			"deployment before cancellation acknowledgement=%+v error=%v",
			deployment,
			err,
		)
	}
	select {
	case <-done:
		t.Fatal("deployment finished while an agent still owned active work")
	case <-time.After(500 * time.Millisecond):
	}
	changed, handled, err := repo.AcknowledgeRemoteStepCancellation(
		ctx,
		repository.RemoteLifecycleClaim{
			DeploymentID:   created.Deployment.ID,
			AgentID:        "a",
			ClaimTokenHash: claimHash[:],
		},
	)
	if err != nil || !changed || !handled {
		t.Fatalf(
			"acknowledge cancellation changed=%v handled=%v error=%v",
			changed,
			handled,
			err,
		)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(
			"deployment did not finish after cancellation acknowledgement",
		)
	}
	runs, err := repo.Queries.ListRemoteStepRuns(ctx,
		db.ListRemoteStepRunsParams{
			DeploymentID: created.Deployment.ID,
			StepIndex:    0,
		})
	if err != nil || len(runs) != 2 || runs[0].State != "cancelled" ||
		runs[1].State != "cancelled" {
		t.Fatalf("remote runs=%+v error=%v", runs, err)
	}
	deployment, err = repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || deployment.Status != "failed" {
		t.Fatalf(
			"deployment after cancellation acknowledgement=%+v error=%v",
			deployment,
			err,
		)
	}
}
