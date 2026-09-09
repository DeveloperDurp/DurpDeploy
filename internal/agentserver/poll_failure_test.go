package agentserver_test

import (
	"net/http"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestPollSealFailureLeavesWaiting(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID := seedPollPayload(t, fixture, "pending", "test-agent")
	if _, err := fixture.repo.DB.Exec(
		"UPDATE agents SET certificate_pem='invalid' WHERE id='test-agent'",
	); err != nil {
		t.Fatal(err)
	}
	response := postAgent(t, fixture, agentproto.PollPath, pollBody)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d want=500", response.StatusCode)
	}
	assertWaitingWithoutPayload(t, fixture, deploymentID)
}

func TestPollWrongAgentGetsNoWork(t *testing.T) {
	fixture := newAgentFixture(t)
	fixture.client.Timeout = 27 * time.Second
	if _, err := fixture.repo.DB.Exec(`INSERT INTO agents
		(id,name,endpoint,status) VALUES ('other-agent','other','https://other','pending')`); err != nil {
		t.Fatal(err)
	}
	deploymentID := seedPollPayload(t, fixture, "pending", "other-agent")
	response := postAgent(t, fixture, agentproto.PollPath, pollBody)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d want=204", response.StatusCode)
	}
	assertWaitingWithoutPayload(t, fixture, deploymentID)
}
