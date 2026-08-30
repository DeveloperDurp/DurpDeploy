package agentserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/events"
)

func TestLifecycle_rejectsStaleStartAfterQueuedCancellation(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a")
	deploymentID := fixture.createWaitingDeployment(t, "payload")
	claimToken := "claim-token"
	claimDispatch(t, fixture, deploymentID, "agent-a", claimToken)
	if _, err := dispatch.NewCancellationService(fixture.repo, nil).Cancel(
		context.Background(),
		deploymentID,
	); err != nil {
		t.Fatalf("cancel queued deployment: %v", err)
	}

	// When
	response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody(claimToken),
	)

	// Then
	if response.StatusCode != http.StatusConflict {
		t.Fatalf(
			"stale start status = %d, want %d",
			response.StatusCode,
			http.StatusConflict,
		)
	}
	assertDispatchState(t, fixture, deploymentID, "cancelled")
	assertDeploymentStatus(t, fixture, deploymentID, "cancelled")
}

func TestLifecycle_runsGuardedStartHeartbeatLogsResult(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a")
	deploymentID := fixture.createWaitingDeployment(t, "payload")
	claimToken := "claim-token"
	claimDispatch(t, fixture, deploymentID, "agent-a", claimToken)

	// When
	start := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody(claimToken),
	)
	heartbeat := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"heartbeat",
		claimBody(claimToken),
	)
	logs := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"logs",
		`{"protocol":"agent/1","claim_token":"claim-token","events":[{"sequence":2,"line":"two"},{"sequence":1,"line":"one"},{"sequence":2,"line":"two"},{"sequence":3,"line":"three"}]}`,
	)
	result := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"result",
		`{"protocol":"agent/1","claim_token":"claim-token","state":"succeeded"}`,
	)

	// Then
	for _, response := range []*http.Response{start, logs, result} {
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"lifecycle status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
	}
	if heartbeat.StatusCode != http.StatusOK {
		t.Fatalf(
			"heartbeat status = %d, want %d",
			heartbeat.StatusCode,
			http.StatusOK,
		)
	}
	var heartbeatResponse agentproto.HeartbeatResponse
	if err := json.NewDecoder(heartbeat.Body).
		Decode(&heartbeatResponse); err != nil {
		t.Fatalf("decode heartbeat: %v", err)
	}
	if heartbeatResponse.CancelRequested ||
		len(heartbeatResponse.ServerPins) != 1 {
		t.Fatalf(
			"heartbeat = %#v, want no cancellation and one server pin",
			heartbeatResponse,
		)
	}
	assertDispatchState(t, fixture, deploymentID, "succeeded")
	assertDeploymentStatus(t, fixture, deploymentID, "succeeded")
	assertLogSequences(t, fixture, deploymentID, []int64{1, 2, 3})
}

func TestLifecycle_rejectsWrongOwnerAndMakesDuplicatesIdempotent(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	otherIdentity := loadTestIdentity(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.activate(t, "agent-b", otherIdentity)
	fixture.addEligibleAgent(t, "agent-a")
	deploymentID := fixture.createWaitingDeployment(t, "payload")
	claimToken := "claim-token"
	claimDispatch(t, fixture, deploymentID, "agent-a", claimToken)

	// When
	start := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody(claimToken),
	)
	duplicate := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody(claimToken),
	)
	wrongOwner := fixture.lifecycle(
		t,
		otherIdentity,
		deploymentID,
		"heartbeat",
		claimBody(claimToken),
	)
	wrongToken := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"heartbeat",
		claimBody("wrong-token"),
	)

	// Then
	for _, response := range []*http.Response{start, duplicate} {
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"idempotent start status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
	}
	for _, response := range []*http.Response{wrongOwner, wrongToken} {
		if response.StatusCode != http.StatusConflict {
			t.Fatalf(
				"wrong owner/token status = %d, want %d",
				response.StatusCode,
				http.StatusConflict,
			)
		}
	}
}

func TestLifecycle_acknowledgesCancellationAndRecordsLateResult(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a")
	deploymentID := fixture.createWaitingDeployment(t, "payload")
	claimToken := "claim-token"
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
			},
			DeploymentID: deploymentID,
			CurrentState: "started",
		},
	); err != nil {
		t.Fatalf("request cancellation: %v", err)
	}

	// When
	heartbeat := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"heartbeat",
		claimBody(claimToken),
	)
	cancelled := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"cancelled",
		claimBody(claimToken),
	)
	duplicate := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"cancelled",
		claimBody(claimToken),
	)
	lateResult := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"result",
		`{"protocol":"agent/1","claim_token":"claim-token","state":"failed","error":"secret"}`,
	)

	// Then
	var response agentproto.HeartbeatResponse
	if err := json.NewDecoder(heartbeat.Body).Decode(&response); err != nil {
		t.Fatalf("decode heartbeat: %v", err)
	}
	if heartbeat.StatusCode != http.StatusOK || !response.CancelRequested {
		t.Fatalf(
			"heartbeat = %d %#v, want cancellation",
			heartbeat.StatusCode,
			response,
		)
	}
	for _, received := range []*http.Response{cancelled, duplicate} {
		if received.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"cancel acknowledgement status = %d, want %d",
				received.StatusCode,
				http.StatusNoContent,
			)
		}
	}
	if lateResult.StatusCode != http.StatusConflict {
		t.Fatalf(
			"late result status = %d, want %d",
			lateResult.StatusCode,
			http.StatusConflict,
		)
	}
	assertDispatchState(t, fixture, deploymentID, "cancelled")
	assertDeploymentStatus(t, fixture, deploymentID, "cancelled")
	assertAgentEventCount(t, fixture, deploymentID, "stale_mutation", 1)
}

func TestLog_rejectsOversizedBatchWithoutPersisting(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a")
	deploymentID := fixture.createWaitingDeployment(t, "payload")
	claimToken := "claim-token"
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
	events := make([]agentproto.LogEvent, agentproto.MaxLogEvents+1)
	for index := range events {
		events[index] = agentproto.LogEvent{
			Sequence: agentproto.LogSequence(index + 1),
			Line:     "line",
		}
	}
	body, err := json.Marshal(
		agentproto.LogBatchRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV1,
			},
			ClaimToken: agentproto.ClaimToken(claimToken),
			Events:     events,
		},
	)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}

	// When
	response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"logs",
		string(body),
	)

	// Then
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"oversized batch status = %d, want %d",
			response.StatusCode,
			http.StatusBadRequest,
		)
	}
	assertLogSequences(t, fixture, deploymentID, nil)
}

func TestLifecycle_publishesOneRedactedFailureNotification(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.listener.events = events.NewBus(fixture.repo)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a")
	deploymentID := fixture.createWaitingDeployment(t, "payload")
	claimToken := "claim-token"
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

	// When
	failed := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"result",
		`{"protocol":"agent/1","claim_token":"claim-token","state":"failed","error":"supersecret"}`,
	)
	duplicate := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"result",
		`{"protocol":"agent/1","claim_token":"claim-token","state":"failed","error":"supersecret"}`,
	)

	// Then
	if failed.StatusCode != http.StatusNoContent ||
		duplicate.StatusCode != http.StatusConflict {
		t.Fatalf(
			"result statuses = %d/%d, want %d/%d",
			failed.StatusCode,
			duplicate.StatusCode,
			http.StatusNoContent,
			http.StatusConflict,
		)
	}
	dispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if dispatch.Reason.String != remoteFailureReason {
		t.Fatalf(
			"failure reason = %q, want static redacted reason",
			dispatch.Reason.String,
		)
	}
	assertNotificationCount(t, fixture, deploymentID, "deployment_failed", 1)
	var message string
	if err := fixture.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT message FROM notification_events WHERE deployment_id = ?",
		deploymentID,
	).Scan(&message); err != nil {
		t.Fatalf("get notification message: %v", err)
	}
	if message != "Remote deployment failed" {
		t.Fatalf(
			"notification message = %q, want remote failure message",
			message,
		)
	}
}
