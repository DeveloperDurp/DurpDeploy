package agentserver

import (
	"encoding/json"
	"testing"

	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestPoll_claimedPayloadOpensOnlyForClaimedIdentity(t *testing.T) {
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a")
	deploymentID := fixture.createWaitingDeployment(t, "payload")
	response := fixture.poll(
		t,
		fixture.agentIdentity,
		`{"protocol":"agent/1","agent_version":"v2"}`,
	)
	var claimed agentproto.PollResponse
	if err := json.NewDecoder(response.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claimed.DeploymentID != agentproto.DeploymentID(deploymentID) {
		t.Fatalf(
			"claim deployment = %d, want %d",
			claimed.DeploymentID,
			deploymentID,
		)
	}
	if _, err := agentpayload.Open(
		loadTestIdentity(t), deploymentID, []byte(claimed.Payload),
	); err == nil {
		t.Fatal("opened claimed envelope with an unassigned identity")
	}
}
