package migrate

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestPostgres_DurableLogSequencesRejectNullAndDuplicates(t *testing.T) {
	assertDurableLogSequences(t, newPostgresTestDBAt(t, 25))
}

func TestMSSQL_DurableLogSequencesRejectNullAndDuplicates(t *testing.T) {
	assertDurableLogSequences(t, newSQLServerTestDBAt(t, 10))
}

func assertDurableLogSequences(t *testing.T, conn *sql.DB) {
	t.Helper()
	if err := goose.Up(conn, "."); err != nil {
		t.Fatalf("apply durable log sequence migration: %v", err)
	}
	seedRemoteAgentDeployment(t, conn)
	if _, err := conn.Exec(
		"INSERT INTO deployment_logs (deployment_id, line, sequence) VALUES (?, ?, ?)",
		1,
		"first",
		1,
	); err != nil {
		t.Fatalf("insert sequenced deployment log: %v", err)
	}
	if _, err := conn.Exec(
		"INSERT INTO deployment_logs (deployment_id, line) VALUES (?, ?)",
		1,
		"missing sequence",
	); err == nil {
		t.Fatal("insert without sequence succeeded")
	}
	if _, err := conn.Exec(
		"INSERT INTO deployment_logs (deployment_id, line, sequence) VALUES (?, ?, ?)",
		1,
		"duplicate sequence",
		1,
	); err == nil {
		t.Fatal("insert with duplicate sequence succeeded")
	}
}
