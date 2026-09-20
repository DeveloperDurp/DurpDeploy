package agentserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestLifecycleHeartbeatRefreshesAgentHealth(t *testing.T) {
	t.Run("remote deployment", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		setOldAgentHeartbeat(t, fixture)
		assertLifecycleHeartbeat(
			t,
			fixture,
			"1",
			`{"protocol":"agent/1","claim_token":"claim-one"}`,
		)
	})
	t.Run("remote step", func(t *testing.T) {
		fixture := newAgentFixture(t)
		deploymentID, claim := claimedRemoteStep(t, fixture)
		setOldAgentHeartbeat(t, fixture)
		assertLifecycleHeartbeat(
			t, fixture, fmt.Sprint(deploymentID), claim,
		)
	})
}

func setOldAgentHeartbeat(t *testing.T, fixture agentFixture) {
	t.Helper()
	if _, err := fixture.repo.DB.Exec(
		"UPDATE agents SET last_heartbeat_at=1, agent_version='v1' " +
			"WHERE id='test-agent'",
	); err != nil {
		t.Fatal(err)
	}
}

func assertLifecycleHeartbeat(
	t *testing.T,
	fixture agentFixture,
	deploymentID string,
	claim string,
) {
	t.Helper()
	start := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.StartPath, "{id}", deploymentID,
	), claim)
	if start.StatusCode != http.StatusNoContent {
		t.Fatalf("start status=%d", start.StatusCode)
	}
	heartbeat := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.HeartbeatPath, "{id}", deploymentID,
	), claim)
	if heartbeat.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status=%d", heartbeat.StatusCode)
	}
	lastHeartbeat, version := agentState(t, fixture.repo)
	if !lastHeartbeat.Valid || lastHeartbeat.Int64 <= 1 {
		t.Fatalf(
			"agent heartbeat=%+v; lifecycle heartbeat was not recorded",
			lastHeartbeat,
		)
	}
	if !version.Valid || version.String != "v1" {
		t.Fatalf("agent version=%+v; lifecycle heartbeat changed it", version)
	}
}
