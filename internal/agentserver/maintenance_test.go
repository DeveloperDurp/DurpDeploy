package agentserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestMaintain_reclaimsOnlyExpiredUnstartedClaims(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a", "")
	deploymentID := fixture.createWaitingDeployment(t, "", "payload")
	hash := sha256.Sum256([]byte("claim-token"))
	if _, err := fixture.repo.Queries.ClaimDeploymentDispatch(
		context.Background(),
		db.ClaimDeploymentDispatchParams{
			AgentID: sql.NullString{
				String: "agent-a",
				Valid:  true,
			}, ClaimTokenHash: hash[:],
			ClaimExpiresAt: sql.NullInt64{
				Int64: fixture.now.Add(-agentproto.PreStartClaimTimeout).Unix(),
				Valid: true,
			},
			LastHeartbeatAt: sql.NullInt64{
				Int64: fixture.now.Unix(),
				Valid: true,
			}, DeploymentID: deploymentID,
		},
	); err != nil {
		t.Fatalf("claim dispatch: %v", err)
	}

	// When
	if err := fixture.listener.Maintain(context.Background()); err != nil {
		t.Fatalf("maintain: %v", err)
	}

	// Then
	assertDispatchState(t, fixture, deploymentID, "waiting")
	assertDeploymentStatus(t, fixture, deploymentID, "pending")
	assertAgentEventCount(t, fixture, deploymentID, "claim_reclaimed", 1)
}

func TestMaintain_losesStartedWorkOnceAcrossRestartAndRecordsLateResult(
	t *testing.T,
) {
	// Given
	fixture := newPollFixture(t)
	fixture.listener.events = events.NewBus(fixture.repo)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a", "")
	deploymentID := fixture.createWaitingDeployment(t, "", "payload")
	const claimToken = "claim-token"
	claimDispatch(t, fixture, deploymentID, "agent-a", claimToken)
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody(claimToken),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"start status = %d, want %d",
			response.StatusCode,
			http.StatusNoContent,
		)
	}
	fixture.now = fixture.now.Add(agentproto.LostThreshold)

	// When
	if err := fixture.listener.Maintain(context.Background()); err != nil {
		t.Fatalf("maintain: %v", err)
	}
	restarted, err := New(
		Config{
			Repository: fixture.repo,
			Identity:   fixture.serverIdentity,
			Now:        func() time.Time { return fixture.now },
			Events:     fixture.listener.events,
			Box:        fixture.box,
		},
	)
	if err != nil {
		t.Fatalf("restart listener: %v", err)
	}
	if err := restarted.Maintain(context.Background()); err != nil {
		t.Fatalf("maintain after restart: %v", err)
	}
	late := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"result",
		`{"protocol":"agent/1","claim_token":"claim-token","state":"failed","error":"secret"}`,
	)

	// Then
	if late.StatusCode != http.StatusConflict {
		t.Fatalf(
			"late result status = %d, want %d",
			late.StatusCode,
			http.StatusConflict,
		)
	}
	assertDispatchState(t, fixture, deploymentID, "lost")
	assertDeploymentStatus(t, fixture, deploymentID, "failed")
	assertAgentEventCount(t, fixture, deploymentID, "deployment_lost", 1)
	assertAgentEventCount(t, fixture, deploymentID, "late_result", 1)
	assertNotificationCount(t, fixture, deploymentID, "deployment_failed", 1)
}

func TestMaintain_marksOfflineAndExpiresCancellationWithoutFailingDeployment(
	t *testing.T,
) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a", "")
	deploymentID := fixture.createWaitingDeployment(t, "", "payload")
	const claimToken = "claim-token"
	claimDispatch(t, fixture, deploymentID, "agent-a", claimToken)
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody(claimToken),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"start status = %d, want %d",
			response.StatusCode,
			http.StatusNoContent,
		)
	}
	if _, err := fixture.repo.Queries.RequestDeploymentDispatchCancellation(
		context.Background(),
		db.RequestDeploymentDispatchCancellationParams{
			CancelRequestedAt: sql.NullInt64{
				Int64: fixture.now.Unix(),
				Valid: true,
			}, DeploymentID: deploymentID, CurrentState: "started",
		},
	); err != nil {
		t.Fatalf("request cancellation: %v", err)
	}
	fixture.now = fixture.now.Add(agentproto.LostThreshold)

	// When
	if err := fixture.listener.Maintain(context.Background()); err != nil {
		t.Fatalf("maintain: %v", err)
	}
	if err := fixture.listener.Maintain(context.Background()); err != nil {
		t.Fatalf("repeat maintain: %v", err)
	}

	// Then
	assertDispatchState(t, fixture, deploymentID, "cancel_unconfirmed")
	assertDeploymentStatus(t, fixture, deploymentID, "running")
	assertAgentEventCount(t, fixture, deploymentID, "cancel_unconfirmed", 1)
	var offline int
	if err := fixture.repo.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM agent_events WHERE agent_id = ? AND event_type = 'agent_offline'", "agent-a").
		Scan(&offline); err != nil {
		t.Fatalf("count offline events: %v", err)
	}
	if offline != 1 {
		t.Fatalf("offline events = %d, want 1", offline)
	}
}

func TestMaintain_keepsHeartbeatingAgentOnline(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a", "")
	deploymentID := fixture.createWaitingDeployment(t, "", "payload")
	const claimToken = "claim-token"
	claimDispatch(t, fixture, deploymentID, "agent-a", claimToken)
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody(claimToken),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"start status = %d, want %d",
			response.StatusCode,
			http.StatusNoContent,
		)
	}
	fixture.now = fixture.now.Add(agentproto.LostThreshold - time.Second)
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"heartbeat",
		claimBody(claimToken),
	); response.StatusCode != http.StatusOK {
		t.Fatalf(
			"heartbeat status = %d, want %d",
			response.StatusCode,
			http.StatusOK,
		)
	}
	fixture.now = fixture.now.Add(time.Second)

	// When
	if err := fixture.listener.Maintain(context.Background()); err != nil {
		t.Fatalf("maintain: %v", err)
	}

	// Then
	assertDispatchState(t, fixture, deploymentID, "started")
	var offline int
	if err := fixture.repo.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM agent_events WHERE agent_id = ? AND event_type = 'agent_offline'", "agent-a").
		Scan(&offline); err != nil {
		t.Fatalf("count offline events: %v", err)
	}
	if offline != 0 {
		t.Fatalf("offline events = %d, want 0", offline)
	}
}
