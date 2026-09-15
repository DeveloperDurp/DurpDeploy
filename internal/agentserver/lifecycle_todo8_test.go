package agentserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStartRemoteDeployment(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)

	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.StartPath, "{id}", "1",
	), `{"protocol":"agent/1","claim_token":"claim-one"}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != "started" || !claim.StartedAt.Valid {
		t.Fatalf("started claim=%+v error=%v", claim, err)
	}
	deployment, err := fixture.repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "running" ||
		!deployment.StartedAt.Valid {
		t.Fatalf("running deployment=%+v error=%v", deployment, err)
	}
}

func TestDuplicateLifecycleDoesNotResetTimestamps(t *testing.T) {
	t.Run("Start", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		path := strings.ReplaceAll(agentproto.StartPath, "{id}", "1")
		body := `{"protocol":"agent/1","claim_token":"claim-one"}`
		first := postAgent(t, fixture, path, body)
		if first.StatusCode != http.StatusNoContent {
			t.Fatalf("first start status=%d", first.StatusCode)
		}
		before, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
			t.Context(),
			1,
		)
		if err != nil {
			t.Fatal(err)
		}
		second := postAgent(t, fixture, path, body)
		if second.StatusCode != http.StatusNoContent {
			t.Fatalf("duplicate start status=%d", second.StatusCode)
		}
		after, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
			t.Context(),
			1,
		)
		if err != nil || after.StartedAt != before.StartedAt ||
			after.ClaimExpiresAt != before.ClaimExpiresAt {
			t.Fatalf(
				"duplicate changed timestamps: before=%+v after=%+v err=%v",
				before,
				after,
				err,
			)
		}
	})

	t.Run("Heartbeat", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		startPath := strings.ReplaceAll(agentproto.StartPath, "{id}", "1")
		start := postAgent(t, fixture, startPath,
			`{"protocol":"agent/1","claim_token":"claim-one"}`)
		if start.StatusCode != http.StatusNoContent {
			t.Fatalf("start status=%d", start.StatusCode)
		}
		path := strings.ReplaceAll(agentproto.HeartbeatPath, "{id}", "1")
		body := `{"protocol":"agent/1","claim_token":"claim-one"}`
		for range 2 {
			response := postAgent(t, fixture, path, body)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("heartbeat status=%d", response.StatusCode)
			}
		}
		claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
			t.Context(),
			1,
		)
		if err != nil || claim.StartedAt != claim.LastHeartbeatAt &&
			claim.StartedAt.Int64 > claim.LastHeartbeatAt.Int64 {
			t.Fatalf(
				"heartbeat reset lifecycle timestamps: %+v err=%v",
				claim,
				err,
			)
		}
	})
}

func TestLifecycleRejectsWrongClaimToken(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.StartPath, "{id}", "1",
	), `{"protocol":"agent/1","claim_token":"wrong-token"}`)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("wrong-token status=%d", response.StatusCode)
	}
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != "claimed" || claim.StartedAt.Valid {
		t.Fatalf("wrong token changed claim=%+v error=%v", claim, err)
	}
}

func TestRemoteCancellationAcknowledgement(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	if _, err := fixture.repo.DB.Exec(`UPDATE remote_deployment_claims SET
		state='waiting',claim_token_hash=NULL,ciphertext=NULL,
		claim_expires_at=NULL,last_heartbeat_at=NULL,updated_at=1
		WHERE deployment_id=1`); err != nil {
		t.Fatal(err)
	}
	activateCancellationFixture(t, fixture.repo)
	heartbeatPath := strings.ReplaceAll(agentproto.HeartbeatPath, "{id}", "2")
	for range 2 {
		heartbeat := postAgent(t, fixture, heartbeatPath,
			`{"protocol":"agent/1","claim_token":"claim-two"}`)
		if heartbeat.StatusCode != http.StatusOK {
			t.Fatalf("cancellation heartbeat status=%d", heartbeat.StatusCode)
		}
		body, err := io.ReadAll(heartbeat.Body)
		if err != nil {
			t.Fatal(err)
		}
		var result agentproto.HeartbeatResponse
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if !result.CancelRequested || len(result.ServerPins) != 1 {
			t.Fatalf("heartbeat response=%+v", result)
		}
	}
	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.CancelledPath, "{id}", "2",
	), `{"protocol":"agent/1","claim_token":"claim-two"}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel acknowledgement status=%d", response.StatusCode)
	}
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(t.Context(), 2)
	if err != nil || claim.State != "cancelled" {
		t.Fatalf("cancelled claim=%+v error=%v", claim, err)
	}
	deployment, err := fixture.repo.Queries.GetDeployment(t.Context(), 2)
	if err != nil || deployment.Status != "cancelled" ||
		!deployment.FinishedAt.Valid {
		t.Fatalf("cancelled deployment=%+v error=%v", deployment, err)
	}
}
