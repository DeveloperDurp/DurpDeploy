package repository_test

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
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
