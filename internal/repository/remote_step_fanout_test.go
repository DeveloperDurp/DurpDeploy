package repository_test

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRemoteStepFanoutTargetsEveryMatchingEnvironmentAgent(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	for _, agentID := range []string{"a", "b"} {
		if _, err := r.Queries.AddAgentEnvironmentLabel(
			ctx,
			db.AddAgentEnvironmentLabelParams{
				AgentID: agentID, EnvironmentID: 2,
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Queries.AddAgentLabel(
			ctx,
			db.AddAgentLabelParams{AgentID: agentID, Label: "other"},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{ID: 3, Status: "running"},
	); err != nil {
		t.Fatal(err)
	}

	created, err := r.QueueRemoteStepRuns(ctx, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 {
		t.Fatalf("created runs = %d, want 2", created)
	}
	runs, err := r.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{DeploymentID: 3, StepIndex: 0},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].AgentID != "a" || runs[1].AgentID != "b" {
		t.Fatalf("runs = %+v, want agents a and b", runs)
	}
}

func TestRemoteStepFanoutRequiresEnvironmentAndCapabilityLabels(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	if _, err := r.Queries.AddAgentEnvironmentLabel(
		ctx,
		db.AddAgentEnvironmentLabelParams{AgentID: "a", EnvironmentID: 2},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Queries.AddAgentLabel(
		ctx,
		db.AddAgentLabelParams{AgentID: "a", Label: "other"},
	); err != nil {
		t.Fatal(err)
	}
	if err := r.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{ID: 3, Status: "running"},
	); err != nil {
		t.Fatal(err)
	}

	created, err := r.QueueRemoteStepRuns(ctx, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("created runs = %d, want 1", created)
	}
	runs, err := r.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{DeploymentID: 3, StepIndex: 0},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].AgentID != "a" {
		t.Fatalf("runs = %+v, want only agent a", runs)
	}
}

func TestRemoteStepFanoutReturnsNoWorkWithoutMatchingAgent(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	if err := r.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{ID: 3, Status: "running"},
	); err != nil {
		t.Fatal(err)
	}

	created, err := r.QueueRemoteStepRuns(ctx, 3, 0)
	if err != nil || created != 0 {
		t.Fatalf("created runs = %d, want 0; error = %v", created, err)
	}
	runs, err := r.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{DeploymentID: 3, StepIndex: 0},
	)
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs = %+v, want none; error = %v", runs, err)
	}
}

func TestRemoteStepFanoutRequiresSnapshotInterpreterCapability(t *testing.T) {
	// Given
	r := remoteFixture(t)
	ctx := context.Background()
	if _, err := r.DB.ExecContext(ctx, `
INSERT INTO agent_environment_labels(agent_id, environment_id) VALUES('a', 2);
INSERT INTO agent_interpreters(agent_id, interpreter) VALUES('b', 'python3');
UPDATE deployments SET status = 'running' WHERE id = 3;
UPDATE deployment_steps SET interpreter = 'python3'
WHERE deployment_id = 3 AND step_index = 0;`); err != nil {
		t.Fatal(err)
	}

	// When
	created, err := r.QueueRemoteStepRuns(ctx, 3, 0)

	// Then
	if err != nil || created != 0 {
		t.Fatalf("created runs=%d want=0; error=%v", created, err)
	}
}

func TestRemoteStepClaimRechecksInterpreterCapability(t *testing.T) {
	// Given
	r := remoteFixture(t)
	ctx := context.Background()
	if _, err := r.DB.ExecContext(ctx, `
INSERT INTO agent_environment_labels(agent_id, environment_id) VALUES('a', 2);
INSERT INTO agent_interpreters(agent_id, interpreter) VALUES('a', 'python3');
INSERT INTO agent_labels(agent_id, label) VALUES('a', 'other');
UPDATE deployments SET status = 'running' WHERE id = 3;
UPDATE deployment_steps SET interpreter = 'python3'
WHERE deployment_id = 3 AND step_index = 0;`); err != nil {
		t.Fatal(err)
	}
	created, err := r.QueueRemoteStepRuns(ctx, 3, 0)
	if err != nil || created != 1 {
		t.Fatalf("queue runs=%d want=1; error=%v", created, err)
	}
	if _, err := r.DB.ExecContext(
		ctx,
		"DELETE FROM agent_interpreters WHERE agent_id = 'a'",
	); err != nil {
		t.Fatal(err)
	}

	// When
	_, claimed, err := r.ClaimRemoteStepPayload(
		ctx,
		"a",
		func(repository.RemotePayloadSnapshot) (
			repository.RemotePreparedClaim,
			error,
		) {
			t.Fatal("incompatible agent reached payload preparation")
			return repository.RemotePreparedClaim{}, nil
		},
	)

	// Then
	if err != nil || claimed {
		t.Fatalf("incompatible claim=%v error=%v", claimed, err)
	}
}
