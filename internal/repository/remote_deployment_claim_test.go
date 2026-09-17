package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestCreateDeploymentSnapshotsAssignedAgent(t *testing.T) {
	repo := remoteFixture(t)
	ctx := context.Background()

	local, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 2, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	if local.Mode != repository.ExecutionLocal ||
		local.Deployment.AssignedAgentID.Valid {
		t.Fatalf("local result=%+v", local)
	}
	if _, err := repo.Queries.GetDeploymentStepSource(
		ctx,
		local.Deployment.ID,
	); err != nil {
		t.Fatalf("local source snapshot: %v", err)
	}
	if _, err := repo.Queries.GetRemoteDeploymentClaim(
		ctx,
		local.Deployment.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("local claim error=%v", err)
	}

	remote, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	if remote.Mode != repository.ExecutionRemote ||
		remote.Deployment.AssignedAgentID.String != "a" {
		t.Fatalf("remote result=%+v", remote)
	}
	claim, err := repo.Queries.GetRemoteDeploymentClaim(
		ctx,
		remote.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.AgentID != "a" || claim.State != "waiting" {
		t.Fatalf("remote claim=%+v", claim)
	}
	waiting, err := repo.Queries.ListWaitingRemoteDeploymentClaims(ctx, "a")
	if err != nil || len(waiting) != 2 {
		t.Fatalf(
			"approval-gated claim became poll eligible: %v %v",
			waiting,
			err,
		)
	}
	approved, err := repo.ApproveDeployment(ctx, db.CreateApprovalParams{
		DeploymentID: remote.Deployment.ID, ApprovedBy: "operator",
		RequiredApproverRole: "admin",
	})
	if err != nil || approved.Mode != repository.ExecutionRemote {
		t.Fatalf("approve remote deployment: %+v %v", approved, err)
	}
	waiting, err = repo.Queries.ListWaitingRemoteDeploymentClaims(ctx, "a")
	if err != nil || len(waiting) != 3 {
		t.Fatalf("approved claim not poll eligible: %v %v", waiting, err)
	}

	before, err := repo.Queries.ListDeployments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Queries.SetAgentStatus(ctx, db.SetAgentStatusParams{
		ID: "a", Status: "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	if !errors.Is(err, repository.ErrDeploymentRoutingConflict) {
		t.Fatalf("inactive assignment error=%v", err)
	}
	after, err := repo.Queries.ListDeployments(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("inactive assignment created row: before=%d after=%d err=%v",
			len(before), len(after), err)
	}
}

func TestCreateDeploymentStepPlacementOverridesLegacyAssignment(t *testing.T) {
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
