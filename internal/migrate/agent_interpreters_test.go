package migrate

import "testing"

func TestAgentInterpretersMigrationConstrainsReportedCapabilities(t *testing.T) {
	// Given
	conn, err := Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(
		"INSERT INTO agents(id, name, endpoint) VALUES('agent', 'Agent', 'https://agent')",
	); err != nil {
		t.Fatal(err)
	}

	// When
	_, validErr := conn.Exec(
		"INSERT INTO agent_interpreters(agent_id, interpreter) VALUES('agent', 'python3')",
	)
	_, invalidErr := conn.Exec(
		"INSERT INTO agent_interpreters(agent_id, interpreter) VALUES('agent', '/bin/sh')",
	)

	// Then
	if validErr != nil {
		t.Fatalf("insert supported interpreter: %v", validErr)
	}
	if invalidErr == nil {
		t.Fatal("insert arbitrary interpreter succeeded")
	}
}
