package dispatch

import (
	"context"
	"database/sql"
	"durpdeploy/internal/db"
	"testing"
)

func TestRoutingPolicy_LegacyEnvironmentAssignmentRoutesRemoteOtherwiseLocal(
	t *testing.T,
) {
	// Given: two legacy deployments without routing snapshots.
	repo, box := newPayloadRepository(t)
	remote := createPayloadDeployment(t, repo)
	ctx := context.Background()
	localEnvironment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "local-environment"},
	)
	if err != nil {
		t.Fatalf("create local environment: %v", err)
	}
	local, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: remote.ReleaseID, EnvironmentID: localEnvironment.ID,
			Status: "pending",
		},
	)
	if err != nil {
		t.Fatalf("create local deployment: %v", err)
	}
	_, err = repo.DB.ExecContext(ctx, `
INSERT INTO agents (
    id, name, status, certificate_pem, certificate_fingerprint
) VALUES ('legacy-agent', 'Legacy', 'active', 'certificate', ?)
`, "legacy-agent0000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("create legacy agent: %v", err)
	}
	_, err = repo.Queries.AssignAgentToEnvironment(
		ctx,
		db.AssignAgentToEnvironmentParams{
			EnvironmentID: remote.EnvironmentID,
			AgentID:       "legacy-agent",
			UpdatedAt:     1,
		},
	)
	if err != nil {
		t.Fatalf("assign legacy agent: %v", err)
	}

	// When: the unchanged legacy dispatcher routes both deployments.
	dispatcher := New(repo, box, nil)
	if err := dispatcher.Dispatch(ctx, remote.ID); err != nil {
		t.Fatalf("dispatch assigned deployment: %v", err)
	}
	if err := dispatcher.Dispatch(ctx, local.ID); err != nil {
		t.Fatalf("dispatch unassigned deployment: %v", err)
	}

	// Then: assignment means remote and absence means local.
	remoteDispatch, err := repo.Queries.GetDeploymentDispatch(ctx, remote.ID)
	if err != nil {
		t.Fatalf("get remote dispatch: %v", err)
	}
	if remoteDispatch.Mode != "remote" ||
		remoteDispatch.AssignedAgentID != (sql.NullString{
			String: "legacy-agent", Valid: true,
		}) {
		t.Fatalf("remote dispatch = %#v", remoteDispatch)
	}
	localDispatch, err := repo.Queries.GetDeploymentDispatch(ctx, local.ID)
	if err != nil {
		t.Fatalf("get local dispatch: %v", err)
	}
	if localDispatch.Mode != "local" || localDispatch.AssignedAgentID.Valid {
		t.Fatalf("local dispatch = %#v", localDispatch)
	}
}
