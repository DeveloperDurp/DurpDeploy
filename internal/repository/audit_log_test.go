package repository_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestAuditLog_Prune(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	// 1. Insert a log entry. It will have created_at = now.
	_, err := repo.Queries.CreateAuditLog(ctx, db.CreateAuditLogParams{
		Action:     "test_action",
		EntityType: "test",
		Details:    sql.NullString{String: "details", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	count, err := repo.Queries.CountAuditLogs(ctx)
	if err != nil || count != 1 {
		t.Fatalf("expected 1 log, got %d (err: %v)", count, err)
	}

	// 2. Prune with a cutoff 1 hour in the past. The log should remain.
	pastCutoff := time.Now().Add(-1 * time.Hour).Unix()
	if err := repo.Queries.PruneAuditLogs(ctx, pastCutoff); err != nil {
		t.Fatalf("prune failed: %v", err)
	}

	count, err = repo.Queries.CountAuditLogs(ctx)
	if err != nil || count != 1 {
		t.Fatalf("expected 1 log to remain, got %d (err: %v)", count, err)
	}

	// 3. Prune with a cutoff 1 hour in the future. The log should be deleted.
	futureCutoff := time.Now().Add(1 * time.Hour).Unix()
	if err := repo.Queries.PruneAuditLogs(ctx, futureCutoff); err != nil {
		t.Fatalf("prune failed: %v", err)
	}

	count, err = repo.Queries.CountAuditLogs(ctx)
	if err != nil || count != 0 {
		t.Fatalf("expected 0 logs to remain, got %d (err: %v)", count, err)
	}
}
