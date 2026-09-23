package agentserver

import (
	"testing"

	"durpdeploy/internal/db"
)

func TestPairingReactivationClearsStaleInterpreterCapabilities(t *testing.T) {
	// Given
	fixture := newPairingFixture(t)
	paired, err := fixture.pair(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.Queries.AddAgentInterpreter(
		t.Context(),
		db.AddAgentInterpreterParams{
			AgentID:     paired.AgentID,
			Interpreter: "python3",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.RevokeAgent(
		t.Context(), paired.AgentID,
	); err != nil {
		t.Fatal(err)
	}
	fixture.requests = nil
	fixture.input = fixture.input.ForAgent(paired.AgentID)

	// When
	if _, err := fixture.pair(t); err != nil {
		t.Fatal(err)
	}

	// Then
	interpreters, err := fixture.repo.Queries.ListAgentInterpreters(
		t.Context(), paired.AgentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(interpreters) != 0 {
		t.Fatalf("reactivated agent kept stale interpreters: %v", interpreters)
	}
}
