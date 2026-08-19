package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPostgres_RemoteAgentSchemaParity(t *testing.T) {
	// Given a disposable PostgreSQL database.
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
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
	_, err := conn.Exec("INSERT INTO agent_pools (name) VALUES (?)", "primary")
	requireNoError(t, err, "create agent pool")
	if _, err := conn.Exec(
		"INSERT INTO agent_pools (name) VALUES (?)",
		"primary",
	); err == nil {
		t.Fatal("duplicate agent pool name succeeded")
	}

	for _, agentID := range []string{"pending-agent-1", "pending-agent-2"} {
		_, err := conn.Exec(
			"INSERT INTO agents (id, name) VALUES (?, ?)",
			agentID,
			agentID,
		)
		requireNoError(t, err, "create pending agent")
	}
	_, err = conn.Exec(`
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
		INSERT INTO agent_pool_memberships (pool_id, agent_id)
		VALUES (1, ?)`, "active-agent")
	requireNoError(t, err, "add agent pool membership")
	if _, err := conn.Exec(`
		INSERT INTO agent_pool_memberships (pool_id, agent_id)
		VALUES (1, ?)`, "active-agent"); err == nil {
		t.Fatal("duplicate agent pool membership succeeded")
	}
	_, err = conn.Exec(`
		INSERT INTO agent_tags (agent_id, tag_key, tag_value)
		VALUES (?, ?, ?)`, "active-agent", "region", "us")
	requireNoError(t, err, "add agent tag")
	if _, err := conn.Exec(`
		INSERT INTO agent_tags (agent_id, tag_key, tag_value)
		VALUES (?, ?, ?)`, "active-agent", "region", "eu"); err == nil {
		t.Fatal("duplicate agent tag key succeeded")
	}
	_, err = conn.Exec(`
		INSERT INTO environment_agent_policies (environment_id, pool_id, selector)
		VALUES (1, 1, ?)`, "region=us")
	requireNoError(t, err, "create environment policy")
	if _, err := conn.Exec(`
		INSERT INTO environment_agent_policies (environment_id, pool_id, selector)
		VALUES (1, 1, ?)`, "region=eu"); err == nil {
		t.Fatal("duplicate environment policy succeeded")
	}

	tokenHash := bytes.Repeat([]byte{1}, 32)
	_, err = conn.Exec(`
		INSERT INTO agent_enrollment_tokens (
			token_hash, agent_id, token_prefix, expires_at
		) VALUES (?, ?, ?, ?)`, tokenHash, "active-agent", "enroll_", 1_700_000_300)
	requireNoError(t, err, "create enrollment token")
	if _, err := conn.Exec(`
		INSERT INTO agent_enrollment_tokens (
			token_hash, agent_id, token_prefix, expires_at
		) VALUES (?, ?, ?, ?)`, tokenHash, "active-agent", "enroll_", 1_700_000_300); err == nil {
		t.Fatal("duplicate enrollment token hash succeeded")
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

	if _, err := conn.Exec("DELETE FROM agent_pools WHERE id = 1"); err == nil {
		t.Fatal("delete of referenced agent pool succeeded")
	}
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
