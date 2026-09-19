package repository

import (
	"context"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestCreateDeploymentRetriesSQLiteBusyUpgrade(t *testing.T) {
	engine := newDeploymentCreationEngine(t, "SQLite")
	creator, locker := openDeploymentCreationEngine(t, engine)
	ctx := context.Background()
	tx, err := locker.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"UPDATE agents SET updated_at=updated_at WHERE id='race-agent'",
	); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		released <- tx.Rollback()
	}()

	result, err := creator.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create deployment while SQLite writer releases: %v", err)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if result.Mode != ExecutionLocal {
		t.Fatalf("execution mode=%q, want %q", result.Mode, ExecutionLocal)
	}
}
