package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestCreateDeploymentSnapshotsAssignedAgent(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "claim-snapshot"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "claim-snapshot"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Queries.CreateAgent(ctx, db.CreateAgentParams{
		ID: "agent-a", Name: "agent-a", Endpoint: "https://agent.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := repo.Queries.AssignEnvironmentAgent(
		ctx,
		db.AssignEnvironmentAgentParams{
			EnvironmentID: environment.ID,
			AgentID:       "agent-a",
		},
	)
	assertOne(t, assigned, err)

	local, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
		AssignedAgentID: sql.NullString{String: "agent-a", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if local.AssignedAgentID.Valid {
		t.Fatalf(
			"local deployment assigned agent %q",
			local.AssignedAgentID.String,
		)
	}
	if !remote.AssignedAgentID.Valid ||
		remote.AssignedAgentID.String != "agent-a" {
		t.Fatalf("remote deployment assignment=%#v", remote.AssignedAgentID)
	}
	persisted, err := repo.Queries.GetDeployment(ctx, remote.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.AssignedAgentID != remote.AssignedAgentID {
		t.Fatalf("persisted assignment=%#v", persisted.AssignedAgentID)
	}
}
