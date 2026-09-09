package agentserver_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"

	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestPollPayloadRoundTrip(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID := seedPollPayload(t, fixture, "pending", "test-agent")

	response := postAgent(t, fixture, agentproto.PollPath, pollBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=%d", response.StatusCode, http.StatusOK)
	}
	poll := decodePollResponse(t, response)
	if int64(poll.DeploymentID) != deploymentID {
		t.Fatalf("deployment=%d want=%d", poll.DeploymentID, deploymentID)
	}
	rawToken, err := base64.RawURLEncoding.DecodeString(string(poll.ClaimToken))
	if err != nil || len(rawToken) != 32 {
		t.Fatalf("claim token is not 32-byte base64url: %v", err)
	}
	plaintext, err := agentpayload.Open(
		fixture.identity,
		deploymentID,
		[]byte(poll.Payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload dispatch.Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Release.Version != "poll-v1" ||
		len(payload.Release.Steps) != 2 ||
		payload.Release.Steps[0].Name != "first" ||
		payload.Release.Steps[1].Name != "second" ||
		len(payload.Variables) != 2 ||
		payload.Variables[0].Value != "override" ||
		payload.Variables[1].Value != "top-secret" ||
		!payload.Variables[1].Secret {
		t.Fatalf("payload=%+v", payload)
	}
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
		response.Request.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(poll.ClaimToken))
	if !bytes.Equal(claim.ClaimTokenHash, wantHash[:]) ||
		claim.Ciphertext.String != poll.Payload ||
		strings.Contains(claim.Ciphertext.String, "top-secret") ||
		strings.Contains(claim.Ciphertext.String, string(poll.ClaimToken)) ||
		claim.ClaimExpiresAt.Int64-claim.LastHeartbeatAt.Int64 != 60 {
		t.Fatalf("persisted claim violates sealed one-time-token contract")
	}
}

func TestLostPollResponseBlocksUntilExpiry(t *testing.T) {
	fixture := newAgentFixture(t)
	firstID := seedPollPayload(t, fixture, "pending", "test-agent")
	secondID := seedPollPayload(t, fixture, "pending", "test-agent")

	first := postAgent(t, fixture, agentproto.PollPath, pollBody)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status=%d", first.StatusCode)
	}
	first.Body.Close()
	fixture.client.Timeout = 27 * time.Second
	second := postAgent(t, fixture, agentproto.PollPath, pollBody)
	if second.StatusCode != http.StatusNoContent {
		t.Fatalf("second status=%d want=204", second.StatusCode)
	}
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
		second.Request.Context(),
		firstID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.State != "claimed" || claim.ClaimExpiresAt.Int64-
		claim.LastHeartbeatAt.Int64 != 60 {
		t.Fatalf("lost response claim=%+v", claim)
	}
	if _, err := fixture.repo.Queries.ExpireRemoteClaims(
		second.Request.Context(),
		claim.ClaimExpiresAt.Int64,
	); err != nil {
		t.Fatal(err)
	}
	third := postAgent(t, fixture, agentproto.PollPath, pollBody)
	poll := decodePollResponse(t, third)
	if int64(poll.DeploymentID) != firstID &&
		int64(poll.DeploymentID) != secondID {
		t.Fatalf("unexpected deployment after expiry: %d", poll.DeploymentID)
	}
}

func TestPollRejectsPendingApproval(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID := seedPollPayload(
		t,
		fixture,
		"pending_approval",
		"test-agent",
	)
	select {
	case <-fixture.repo.RemoteWorkReady():
		t.Fatal("pending approval emitted remote-work notification")
	default:
	}

	_, claimed, err := fixture.repo.ClaimRemoteDeploymentPayload(
		t.Context(),
		"test-agent",
		func(repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
			t.Fatal("pending approval reached payload preparation")
			return repository.RemotePreparedClaim{}, nil
		},
	)
	if err != nil || claimed {
		t.Fatalf("pre-approval claim succeeded=%v error=%v", claimed, err)
	}
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
		t.Context(),
		deploymentID,
	)
	if err != nil || claim.State != "waiting" {
		t.Fatalf("pre-approval claim=%+v error=%v", claim, err)
	}
	if _, err := fixture.repo.ApproveDeployment(
		t.Context(),
		db.CreateApprovalParams{
			DeploymentID:         deploymentID,
			ApprovedBy:           "operator",
			RequiredApproverRole: "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	response := postAgent(t, fixture, agentproto.PollPath, pollBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("post-approval status=%d", response.StatusCode)
	}
}

func TestPollNoWorkReturnsWithinDeadline(t *testing.T) {
	fixture := newAgentFixture(t)
	fixture.client.Timeout = 27 * time.Second
	started := time.Now()
	response := postAgent(t, fixture, agentproto.PollPath, pollBody)
	elapsed := time.Since(started)
	if response.StatusCode != http.StatusNoContent || elapsed > 26*time.Second {
		t.Fatalf("status=%d elapsed=%s", response.StatusCode, elapsed)
	}
}
