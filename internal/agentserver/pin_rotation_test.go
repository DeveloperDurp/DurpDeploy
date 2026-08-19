package agentserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"durpdeploy/internal/agentproto"
)

func TestHeartbeat_advertisesConfiguredPendingServerPin(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	pendingIdentity := loadTestIdentity(t)
	fixture.listener.pendingServerPin = &pendingIdentity.Fingerprint
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a", "")
	deploymentID := fixture.createWaitingDeployment(t, "", "payload")
	claimDispatch(t, fixture, deploymentID, "agent-a", "claim-token")
	started := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"start",
		claimBody("claim-token"),
	)
	if started.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"start status = %d, want %d",
			started.StatusCode,
			http.StatusNoContent,
		)
	}

	// When
	heartbeat := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		deploymentID,
		"heartbeat",
		claimBody("claim-token"),
	)

	// Then
	var response agentproto.HeartbeatResponse
	if err := json.NewDecoder(heartbeat.Body).Decode(&response); err != nil {
		t.Fatalf("decode heartbeat: %v", err)
	}
	want := []agentproto.CertificateFingerprint{
		agentproto.CertificateFingerprint(
			fixture.serverIdentity.Fingerprint.String(),
		),
		agentproto.CertificateFingerprint(pendingIdentity.Fingerprint.String()),
	}
	if heartbeat.StatusCode != http.StatusOK ||
		len(response.ServerPins) != len(want) {
		t.Fatalf(
			"heartbeat = %d %#v, want two pins",
			heartbeat.StatusCode,
			response,
		)
	}
	for index := range want {
		if response.ServerPins[index] != want[index] {
			t.Fatalf(
				"server pin %d = %q, want %q",
				index,
				response.ServerPins[index],
				want[index],
			)
		}
	}
}
