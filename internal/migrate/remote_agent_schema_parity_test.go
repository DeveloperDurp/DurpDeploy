package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgres_RemoteAgentSchemaParity(t *testing.T) {
	// Given a disposable PostgreSQL database.
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("could not start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL DSN: %v", err)
	}
	conn, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// When the remote-agent persistence contract is exercised.
	assertRemoteAgentTables(t, conn, "public")
	assertRemoteAgentIndexes(t, conn, `
		SELECT COUNT(*) FROM pg_indexes
		WHERE schemaname = 'public' AND indexname = ?`)
	assertRemoteAgentPersistence(t, conn)
}

func TestMSSQL_RemoteAgentSchemaParity(t *testing.T) {
	// Given a native SQL Server database.
	conn := newSQLServerTestDB(t)

	// When the remote-agent persistence contract is exercised.
	assertRemoteAgentTables(t, conn, "dbo")
	assertRemoteAgentIndexes(
		t,
		conn,
		"SELECT COUNT(*) FROM sys.indexes WHERE name = ?",
	)
	assertRemoteAgentPersistence(t, conn)
}

func assertRemoteAgentTables(t *testing.T, conn *sql.DB, schema string) {
	t.Helper()
	for _, table := range remoteAgentTableNames {
		var count int
		err := conn.QueryRow(`
			SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = ? AND table_name = ?`, schema, table).Scan(&count)
		requireNoError(t, err, "find remote-agent table "+table)
		if count != 1 {
			t.Fatalf("remote-agent table %s count = %d, want 1", table, count)
		}
	}
}

func assertRemoteAgentIndexes(t *testing.T, conn *sql.DB, query string) {
	t.Helper()
	for _, index := range remoteAgentIndexNames {
		var count int
		err := conn.QueryRow(query, index).Scan(&count)
		requireNoError(t, err, "find remote-agent index "+index)
		if count != 1 {
			t.Fatalf("remote-agent index %s count = %d, want 1", index, count)
		}
	}
}

func assertRemoteAgentPersistence(t *testing.T, conn *sql.DB) {
	t.Helper()
	seedRemoteAgentDeployment(t, conn)

	const fingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, agentID := range []string{"pending-agent-1", "pending-agent-2"} {
		_, err := conn.Exec(
			"INSERT INTO agents (id, name) VALUES (?, ?)",
			agentID,
			agentID,
		)
		requireNoError(t, err, "create pending agent")
	}
	_, err := conn.Exec(`
		INSERT INTO agents (
			id, name, status, agent_version, certificate_pem,
			certificate_fingerprint
		) VALUES (?, ?, 'active', ?, ?, ?)`,
		"active-agent",
		"Active Agent",
		"agent/1",
		"-----BEGIN CERTIFICATE-----",
		fingerprint,
	)
	requireNoError(t, err, "create active agent")
	if _, err := conn.Exec(`
		INSERT INTO agents (
			id, name, status, certificate_pem, certificate_fingerprint
		) VALUES (?, ?, 'active', ?, ?)`,
		"duplicate-fingerprint-agent",
		"Duplicate Fingerprint Agent",
		"-----BEGIN CERTIFICATE-----",
		fingerprint,
	); err == nil {
		t.Fatal("duplicate agent certificate fingerprint succeeded")
	}

	_, err = conn.Exec(`
		INSERT INTO deployment_payloads (deployment_id, ciphertext)
		VALUES (1, ?)`, "ciphertext")
	requireNoError(t, err, "create deployment payload")
	if _, err := conn.Exec(`
		INSERT INTO deployment_payloads (deployment_id, ciphertext)
		VALUES (1, ?)`, "ciphertext"); err == nil {
		t.Fatal("duplicate deployment payload succeeded")
	}
	assertRemoteAgentDispatchAndLogConstraints(t, conn)
	assertPullAgentPairingPersistence(t, conn)

	if _, err := conn.Exec(
		"DELETE FROM agents WHERE id = ?",
		"active-agent",
	); err == nil {
		t.Fatal("delete of referenced agent succeeded")
	}
	if _, err := conn.Exec("DELETE FROM deployments WHERE id = 1"); err == nil {
		t.Fatal("delete of payload and event history succeeded")
	}
}

func assertPullAgentPairingPersistence(t *testing.T, conn *sql.DB) {
	t.Helper()
	codeHash := bytes.Repeat([]byte{3}, 32)
	agentPin := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	serverPin := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	_, err := conn.Exec(`
		INSERT INTO agent_pairings (
			agent_id, pairing_code_hash, agent_public_identity, agent_pin,
			server_public_identity, server_pin, state, expires_at, paired_at
		) VALUES (?, ?, ?, ?, ?, ?, 'paired', ?, ?)`,
		"pending-agent-1",
		codeHash,
		"agent-public-identity",
		agentPin,
		"server-public-identity",
		serverPin,
		1_700_000_300,
		1_700_000_200,
	)
	requireNoError(t, err, "create paired agent persistence")
	_, err = conn.Exec(`
		INSERT INTO agent_pairings (
			agent_id, pairing_code_hash, agent_public_identity, agent_pin,
			state, expires_at
		) VALUES (?, ?, ?, ?, 'committing', ?)`,
		"pending-agent-2",
		bytes.Repeat([]byte{6}, 32),
		"committing-agent-public-identity",
		"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		1_700_000_300,
	)
	requireNoError(t, err, "create committing agent persistence")
	_, err = conn.Exec(`
		INSERT INTO environment_agent_assignments (environment_id, agent_id)
		VALUES (1, ?)`, "pending-agent-1")
	requireNoError(t, err, "create direct environment assignment")
	if _, err := conn.Exec(`
		INSERT INTO environment_agent_assignments (environment_id, agent_id)
		VALUES (1, ?)`, "pending-agent-2"); err == nil {
		t.Fatal("second direct environment assignment succeeded")
	}

	for range 2 {
		_, err = conn.Exec(`
			INSERT INTO deployments (release_id, environment_id, status)
			VALUES (1, 1, 'pending')`)
		requireNoError(t, err, "create direct deployment")
	}
	_, err = conn.Exec(`
		INSERT INTO deployment_dispatches (
			deployment_id, mode, state, assigned_agent_id, agent_id,
			claim_token_hash, claim_expires_at
		) VALUES (4, 'remote', 'claimed', ?, ?, ?, ?)`,
		"pending-agent-1",
		"pending-agent-1",
		bytes.Repeat([]byte{4}, 32),
		1_700_000_300,
	)
	requireNoError(t, err, "create active direct claim")
	if _, err := conn.Exec(`
		INSERT INTO deployment_dispatches (
			deployment_id, mode, state, assigned_agent_id, agent_id,
			claim_token_hash, claim_expires_at
		) VALUES (5, 'remote', 'claimed', ?, ?, ?, ?)`,
		"pending-agent-1",
		"pending-agent-1",
		bytes.Repeat([]byte{5}, 32),
		1_700_000_300,
	); err == nil {
		t.Fatal("second active direct claim succeeded")
	}
}
