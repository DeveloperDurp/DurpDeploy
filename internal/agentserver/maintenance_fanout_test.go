package agentserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
)

func TestMaintain_failsInactiveAgentFanoutChildrenAndRecomputesParent(
	t *testing.T,
) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	agentB := loadTestIdentity(t)
	fixture.activate(t, "agent-b", agentB)
	parent, err := fixture.repo.Queries.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID:     fixture.releaseID,
			EnvironmentID: fixture.envID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	createChild := func(agentID string) int64 {
		t.Helper()
		child, err := fixture.repo.Queries.CreateRoutingChildDeployment(
			context.Background(),
			db.CreateRoutingChildDeploymentParams{
				ReleaseID:     fixture.releaseID,
				EnvironmentID: fixture.envID,
				Status:        "pending",
				ParentDeploymentID: sql.NullInt64{
					Int64: parent.ID,
					Valid: true,
				},
				TargetAgentID:   sql.NullString{String: agentID, Valid: true},
				TargetAgentName: sql.NullString{String: agentID, Valid: true},
			},
		)
		if err != nil {
			t.Fatalf("create child: %v", err)
		}
		if _, err := fixture.repo.Queries.CreateDirectDeploymentDispatch(
			context.Background(),
			db.CreateDirectDeploymentDispatchParams{
				DeploymentID:    child.ID,
				AssignedAgentID: sql.NullString{String: agentID, Valid: true},
			},
		); err != nil {
			t.Fatalf("create child dispatch: %v", err)
		}
		return child.ID
	}
	claimedChild := createChild("agent-a")
	waitingChild := createChild("agent-a")
	startedChild := createChild("agent-b")
	claimedHash := sha256.Sum256([]byte("expired-claim"))
	if _, err := fixture.repo.Queries.ClaimDeploymentDispatch(
		context.Background(),
		db.ClaimDeploymentDispatchParams{
			AgentID:         sql.NullString{String: "agent-a", Valid: true},
			ClaimTokenHash:  claimedHash[:],
			ClaimExpiresAt:  sql.NullInt64{Int64: fixture.now.Unix(), Valid: true},
			LastHeartbeatAt: sql.NullInt64{Int64: fixture.now.Unix(), Valid: true},
			DeploymentID:    claimedChild,
		},
	); err != nil {
		t.Fatalf("claim child: %v", err)
	}
	const startedToken = "started-claim"
	claimDispatch(t, fixture, startedChild, "agent-b", startedToken)
	if response := fixture.lifecycle(
		t,
		agentB,
		startedChild,
		"start",
		claimBody(startedToken),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"start child status = %d, want %d",
			response.StatusCode,
			http.StatusNoContent,
		)
	}
	fixture.revoke(t, "agent-a")

	// When
	if err := fixture.listener.Maintain(context.Background()); err != nil {
		t.Fatalf("maintain: %v", err)
	}

	// Then
	assertDispatchState(t, fixture, claimedChild, "failed")
	assertDispatchState(t, fixture, waitingChild, "failed")
	assertDeploymentStatus(t, fixture, claimedChild, "failed")
	assertDeploymentStatus(t, fixture, waitingChild, "failed")
	aggregate, err := fixture.repo.Queries.GetDeployment(
		context.Background(),
		parent.ID,
	)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if aggregate.Status != "running" || !aggregate.StartedAt.Valid ||
		aggregate.FinishedAt.Valid {
		t.Fatalf(
			"parent aggregate = %#v, want running and unfinished",
			aggregate,
		)
	}
	t.Logf(
		"inactive fanout: parent=%d status=running claimed_child=%d waiting_child=%d both=failed",
		parent.ID,
		claimedChild,
		waitingChild,
	)
}
