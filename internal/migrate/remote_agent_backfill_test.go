package migrate

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestRemoteAgentBackfill_RefusesNonWaitingNullAgent(t *testing.T) {
	conn := newRemoteAgentSQLiteDB(t)
	migrateRemoteAgentSQLiteTo(t, conn, 27)
	seedDirectDispatchBackfillFixture(t, conn, false)
	seedLatestEnvironmentAssignment(t, conn)
	_, err := conn.Exec(`
		UPDATE deployment_dispatches
		SET agent_id = NULL
		WHERE deployment_id = 2`)
	if err != nil {
		t.Fatalf("clear claimed dispatch agent: %v", err)
	}
	if err := goose.UpTo(conn, ".", 28); err == nil || !strings.Contains(
		err.Error(),
		"cannot migrate pooled deployment dispatches for environments: 1",
	) {
		t.Fatalf("non-waiting null-agent migration error = %v", err)
	}
}

func TestRemoteAgentBackfill_PartialBackupPreservesEnrollmentTokens(t *testing.T) {
	conn := newRemoteAgentSQLiteDB(t)
	migrateRemoteAgentSQLiteTo(t, conn, 28)
	_, err := conn.Exec(`DROP TABLE direct_dispatch_backup_agent_tags`)
	if err != nil {
		t.Fatalf("remove one backup table: %v", err)
	}
	if err := goose.UpTo(conn, ".", 29); err == nil || !strings.Contains(
		err.Error(), "rollback backups are missing") {
		t.Fatalf("partial backup migration error = %v", err)
	}
	var count int
	if err := conn.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_enrollment_tokens'",
	).Scan(&count); err != nil {
		t.Fatalf("read enrollment table after refusal: %v", err)
	}
	if count != 1 {
		t.Fatalf("enrollment table count after refusal = %d, want 1", count)
	}
}
