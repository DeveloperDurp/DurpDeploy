package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

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
