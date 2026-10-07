package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestDeletedPairingRejectsPreDeletionSnapshotPostgreSQL(t *testing.T) {
	engine := newDeploymentCreationEngine(t, "PostgreSQL")
	first, second := openDeploymentCreationEngine(t, engine)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := first.RevokeAgent(ctx, "race-agent"); err != nil {
		t.Fatal(err)
	}
	deletion, err := first.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer deletion.Rollback()
	if rows, err := first.Queries.WithTx(deletion).MarkAgentDeleted(
		ctx, "race-agent",
	); err != nil || rows != 1 {
		t.Fatalf("delete rows=%d error=%v", rows, err)
	}
	if _, err := deletion.ExecContext(ctx, `UPDATE agent_pairings
		SET updated_at=updated_at WHERE agent_id='race-agent'`); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- second.WithTx(ctx, func(q *db.Queries) error {
			_, err := q.ResetRevokedAgentPairing(ctx,
				db.ResetRevokedAgentPairingParams{
					AgentID: "race-agent", PairingCodeHash: make([]byte, 32),
					AgentPublicIdentity: "public", AgentPin: strings.Repeat("b", 64),
					ServerPublicIdentity: raceString("server"),
					ServerPin:            raceString(strings.Repeat("c", 64)),
					EncryptedIdentity:    raceString("cipher"), Now: 200, ExpiresAt: 500,
				})
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPairingTupleConflict
			}
			if err != nil {
				return err
			}
			rows, err := q.ResetRevokedAgentForPairing(ctx,
				db.ResetRevokedAgentForPairingParams{
					ID: "race-agent", Endpoint: "https://agent.invalid",
					PairingCodeHash: make([]byte, 32),
				})
			if err != nil {
				return err
			}
			if rows != 1 {
				return ErrPairingTupleConflict
			}
			return nil
		})
	}()
	monitor, err := sql.Open(engine.driver, engine.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked int
		if err := monitor.QueryRowContext(ctx, `SELECT COUNT(*)
			FROM pg_stat_activity WHERE wait_event_type='Lock'
			AND query LIKE '%UPDATE agent_pairings SET%'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("reset did not wait on deletion: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := deletion.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrPairingTupleConflict) {
		t.Fatalf("pre-deletion snapshot restored consumed code: %v", err)
	}
	agent, err := first.Queries.GetAgentForPairing(ctx, "race-agent")
	if err != nil || !agent.DeletedAt.Valid || agent.Status != "revoked" {
		t.Fatalf("deleted agent=%+v error=%v", agent, err)
	}
}
