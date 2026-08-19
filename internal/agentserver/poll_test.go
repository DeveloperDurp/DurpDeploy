package agentserver

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"durpdeploy/internal/agentpayload"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
)

func TestPoll_claimsOldestEligibleDeployment(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.addEligibleAgent(t, "agent-a", "region=us")
	fixture.createWaitingDeployment(t, "region=us", "payload-a")
	fixture.createWaitingDeployment(t, "region=us", "payload-b")

	// When
	response := fixture.poll(
		t,
		fixture.agentIdentity,
		`{"protocol":"agent/1","agent_version":"v2"}`,
	)

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"poll status = %d, want %d",
			response.StatusCode,
			http.StatusOK,
		)
	}
	var claimed agentproto.PollResponse
	if err := json.NewDecoder(response.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	plaintext, err := agentpayload.Open(
		fixture.agentIdentity,
		1,
		[]byte(claimed.Payload),
	)
	if err != nil {
		t.Fatalf("open claimed envelope: %v", err)
	}
	if claimed.DeploymentID != 1 || string(plaintext) != "payload-a" ||
		claimed.ClaimToken == "" {
		t.Fatalf("claim = %#v, want oldest typed payload and token", claimed)
	}
	hash := sha256.Sum256([]byte(claimed.ClaimToken))
	fixture.assertClaim(t, 1, "agent-a", hash[:])
	fixture.assertAgentVersion(t, "agent-a", "v2")
}

func TestPoll_rejectsUnauthenticatedOrIneligibleAgents(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*pollFixture)
		body    string
		status  int
	}{
		{
			name: "wrong pool",
			prepare: func(fixture *pollFixture) {
				fixture.activate(t, "agent-a", fixture.agentIdentity)
				fixture.addPoolMember(t, "other-pool", "agent-a")
				fixture.createWaitingDeployment(t, "region=us", "payload")
			},
			body:   `{"protocol":"agent/1","agent_version":"v1"}`,
			status: http.StatusNoContent,
		},
		{
			name: "wrong tags",
			prepare: func(fixture *pollFixture) {
				fixture.activate(t, "agent-a", fixture.agentIdentity)
				fixture.addEligibleAgent(t, "agent-a", "region=eu")
				fixture.createWaitingDeployment(t, "region=us", "payload")
			},
			body:   `{"protocol":"agent/1","agent_version":"v1"}`,
			status: http.StatusNoContent,
		},
		{
			name: "revoked certificate",
			prepare: func(fixture *pollFixture) {
				fixture.activate(t, "agent-a", fixture.agentIdentity)
				fixture.revoke(t, "agent-a")
			},
			body:   `{"protocol":"agent/1","agent_version":"v1"}`,
			status: http.StatusUnauthorized,
		},
		{
			name: "unsupported protocol",
			prepare: func(fixture *pollFixture) {
				fixture.activate(t, "agent-a", fixture.agentIdentity)
			},
			body:   `{"protocol":"agent/2","agent_version":"v1"}`,
			status: http.StatusBadRequest,
		},
		{
			name: "active deployment",
			prepare: func(fixture *pollFixture) {
				fixture.activate(t, "agent-a", fixture.agentIdentity)
				fixture.addEligibleAgent(t, "agent-a", "region=us")
				fixture.createActiveClaim(t, "agent-a")
				fixture.createWaitingDeployment(t, "region=us", "payload")
			},
			body:   `{"protocol":"agent/1","agent_version":"v1"}`,
			status: http.StatusNoContent,
		},
		{
			name: "no matching deployment",
			prepare: func(fixture *pollFixture) {
				fixture.activate(t, "agent-a", fixture.agentIdentity)
			},
			body:   `{"protocol":"agent/1","agent_version":"v1"}`,
			status: http.StatusNoContent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newPollFixture(t)
			test.prepare(fixture)

			// When
			response := fixture.poll(t, fixture.agentIdentity, test.body)

			// Then
			if response.StatusCode != test.status {
				t.Fatalf(
					"poll status = %d, want %d",
					response.StatusCode,
					test.status,
				)
			}
		})
	}
}

func TestPoll_allowsOnlyOneConcurrentClaim(t *testing.T) {
	// Given
	fixture := newPollFixture(t)
	secondIdentity := loadTestIdentity(t)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	fixture.activate(t, "agent-b", secondIdentity)
	fixture.addEligibleAgent(t, "agent-a", "region=us")
	fixture.addPoolMember(t, fixture.poolName, "agent-b")
	fixture.setTag(t, "agent-b", "region", "us")
	fixture.createWaitingDeployment(t, "region=us", "payload")
	start := make(chan struct{})
	results := make(chan int, 2)
	var waitGroup sync.WaitGroup
	for _, identity := range []agenttls.Identity{
		fixture.agentIdentity,
		secondIdentity,
	} {
		waitGroup.Add(1)
		go func(identity agenttls.Identity) {
			defer waitGroup.Done()
			<-start
			results <- fixture.pollStatus(identity)
		}(identity)
	}

	// When
	close(start)
	waitGroup.Wait()
	close(results)

	// Then
	claims := 0
	for status := range results {
		if status == http.StatusOK {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("successful concurrent claims = %d, want 1", claims)
	}
}
