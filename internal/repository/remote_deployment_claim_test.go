package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestCreateDeploymentRejectsWholeDeploymentRouting(t *testing.T) {
	repo := remoteFixture(t)
	ctx := context.Background()

	result, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
		AssignedAgentID: sql.NullString{String: "a", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != repository.ExecutionLocal ||
		result.Deployment.AssignedAgentID.Valid {
		t.Fatalf("legacy release result=%+v", result)
	}
	if _, err := repo.Queries.GetDeploymentStepSource(
		ctx,
		result.Deployment.ID,
	); err != nil {
		t.Fatalf("legacy release source snapshot: %v", err)
	}
	if _, err := repo.Queries.GetRemoteDeploymentClaim(
		ctx,
		result.Deployment.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("whole-deployment claim error=%v", err)
	}
}

func TestCreateDeploymentKeepsStepRoutingAtStepLevel(t *testing.T) {
	repo := remoteFixture(t)
	ctx := context.Background()
	_, err := repo.Queries.UpdateRelease(ctx, db.UpdateReleaseParams{
		ID:        1,
		ProjectID: 1,
		Version:   "v1",
		StepsJson: `[{"name":"remote","execution_target":"agent",` +
			`"agent_selectors":["linux"]}]`,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != repository.ExecutionLocal ||
		result.Deployment.AssignedAgentID.Valid {
		t.Fatalf("step-routed result=%+v", result)
	}
	if _, err := repo.Queries.GetRemoteDeploymentClaim(
		ctx,
		result.Deployment.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy whole-deployment claim error=%v", err)
	}
}
