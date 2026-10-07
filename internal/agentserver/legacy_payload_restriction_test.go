package agentserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/dispatch"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestLegacyDifferingRestrictionsFailBeforeClaimWithoutPoisoningPoll(
	t *testing.T,
) {
	f := newAgentFixture(t)
	id := seedPollPayload(t, f, "pending", "test-agent")
	if _, err := f.repo.DB.ExecContext(t.Context(), `
        UPDATE deployment_steps SET variable_names=CASE step_index
            WHEN 0 THEN '["MODE"]' ELSE '["TOKEN"]' END WHERE deployment_id=?`, id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	request := agentproto.PollRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: agentproto.AgentV1,
		},
	}
	recordFixturePoll(t, f, request)
	_, claimed, err := dispatch.New(f.repo).Poll(ctx, "test-agent", request)
	if claimed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claimed=%v error=%v", claimed, err)
	}
	claim, err := f.repo.Queries.GetRemoteDeploymentClaim(t.Context(), id)
	if err != nil || claim.State != "failed" ||
		claim.Reason.String != "remote_payload_requires_agent_3" ||
		claim.ClaimTokenHash != nil ||
		claim.Ciphertext.Valid {
		t.Fatalf("claim=%+v error=%v", claim, err)
	}
	assertDeploymentStatus(t, f, id, "failed")
	next := seedPollPayload(t, f, "pending", "test-agent")
	response, claimed, err := dispatch.New(f.repo).
		Poll(t.Context(), "test-agent", request)
	if err != nil || !claimed || int64(response.DeploymentID) != next {
		t.Fatalf(
			"healthy claim=%v response=%+v error=%v",
			claimed,
			response,
			err,
		)
	}
}

func TestLegacyEqualRestrictionsRemainRepresentable(t *testing.T) {
	f := newAgentFixture(t)
	id := seedPollPayload(t, f, "pending", "test-agent")
	if _, err := f.repo.DB.ExecContext(t.Context(),
		`UPDATE deployment_steps SET variable_names='["MODE"]' WHERE deployment_id=?`, id); err != nil {
		t.Fatal(err)
	}
	request := agentproto.PollRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: agentproto.AgentV1,
		},
	}
	recordFixturePoll(t, f, request)
	_, claimed, err := dispatch.New(f.repo).
		Poll(t.Context(), "test-agent", request)
	if err != nil || !claimed {
		t.Fatalf("claim=%v error=%v", claimed, err)
	}
}
