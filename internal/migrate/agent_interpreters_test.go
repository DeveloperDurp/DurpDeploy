package migrate

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/migrations"
)

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

func TestAgentInterpretersMigrationBackfillsBashForPairedAgents(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "agent-interpreters.db")
	conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(conn, ".", 35); err != nil {
		t.Fatal(err)
	}
	pin := strings.Repeat("a", 64)
	if _, err := conn.Exec(`
INSERT INTO agents(
    id, name, endpoint, status, certificate_pem,
    certificate_fingerprint, encrypted_identity
) VALUES('legacy', 'Legacy', 'https://legacy', 'active', 'certificate', ?, 'identity')`,
		pin); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
INSERT INTO agent_pairings(
    agent_id, pairing_code_hash, agent_public_identity, agent_pin,
    server_public_identity, server_pin, encrypted_identity,
    state, expires_at, paired_at
) VALUES('legacy', ?, 'agent-public', ?, 'server-public', ?, 'identity',
	'paired', 200, 100)`, bytes.Repeat([]byte{1}, 32), pin, pin); err != nil {
		t.Fatal(err)
	}

	// When
	if err := goose.UpTo(conn, ".", 36); err != nil {
		t.Fatal(err)
	}

	// Then
	var interpreter string
	if err := conn.QueryRow(`SELECT interpreter FROM agent_interpreters
WHERE agent_id = 'legacy'`).Scan(&interpreter); err != nil {
		t.Fatal(err)
	}
	if interpreter != "bash" {
		t.Fatalf("interpreter=%q want=bash", interpreter)
	}
}
