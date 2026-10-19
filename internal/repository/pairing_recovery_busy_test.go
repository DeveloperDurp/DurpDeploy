package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/testutil"
)

const recoveryAgentID = "pairing-old"

func pairingRecoveryTuple(
	t *testing.T,
	repo *Repository,
	state string,
) AgentPairingTuple {
	t.Helper()
	old := busyPairingTuple()
	old.AgentID = recoveryAgentID
	old.PairingCodeHash = bytes.Repeat([]byte{8}, 32)
	if _, err := repo.PrepareAgentPairing(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if state == "revoked" {
		if _, err := repo.RevokeAgent(t.Context(), old.AgentID); err != nil {
			t.Fatal(err)
		}
	} else if _, err := repo.DB.ExecContext(t.Context(),
		"UPDATE agent_pairings SET state='expired' WHERE agent_id=?", old.AgentID); err != nil {
		t.Fatal(err)
	}
	tuple := busyPairingTuple()
	if state == "revoked" {
		tuple.ExpectedAgentID = old.AgentID
	}
	return tuple
}

func TestPrepareAgentPairingRecoveryRetriesSQLiteBusy(t *testing.T) {
	for _, state := range []string{"expired", "revoked"} {
		for _, cancel := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("%s/cancel=%t", state, cancel),
				func(t *testing.T) {
					checkPairingRecoveryBusy(t, state, cancel)
				},
			)
		}
	}
}

func checkPairingRecoveryBusy(t *testing.T, state string, cancel bool) {
	t.Helper()
	creator, locker := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	tuple := pairingRecoveryTuple(t, creator, state)
	lock := holdClaimWriter(t, locker.DB)
	defer testutil.Rollback(t, lock)
	ctx := t.Context()
	if cancel {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, 50*time.Millisecond)
		defer stop()
	} else {
		defer releasePairingWriter(t, lock)()
	}
	pairing, err := creator.PrepareAgentPairing(ctx, tuple)
	if cancel {
		checkPairingBusyCancellation(t, ctx, err, pairing.AgentID)
	} else {
		wantID := tuple.AgentID
		if state == "revoked" {
			wantID = tuple.ExpectedAgentID
		}
		if err != nil || pairing.AgentID != wantID || pairing.State != "committing" {
			t.Fatalf("pairing=%+v error=%v", pairing, err)
		}
	}
	var count int
	if err := creator.DB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM agent_pairings WHERE agent_pin=?",
		tuple.AgentPin).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pairings=%d error=%v", count, err)
	}
}

func checkPairingBusyCancellation(
	t *testing.T,
	ctx context.Context,
	err error,
	id string,
) {
	t.Helper()
	if err == nil || ctx.Err() != context.DeadlineExceeded ||
		errors.Is(err, ErrPairingTupleConflict) || id != "" {
		t.Fatalf("agent=%q error=%v context=%v", id, err, ctx.Err())
	}
}

func releasePairingWriter(t *testing.T, lock *sql.Tx) func() {
	t.Helper()
	released := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		released <- lock.Rollback()
	}()
	return func() {
		if err := <-released; err != nil {
			t.Error(err)
		}
	}
}

func TestPrepareAgentPairingRecoveryPreservesDatabaseErrors(t *testing.T) {
	for _, scenario := range []struct{ name, state, operation string }{
		{"reset-revoked", "revoked", "UPDATE ON agents"},
		{"delete-expired", "expired", "DELETE ON agent_pairings"},
		{"delete-pending", "expired", "DELETE ON agents"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			checkPairingRecoveryDatabaseError(
				t,
				scenario.state,
				scenario.operation,
			)
		})
	}
}

func checkPairingRecoveryDatabaseError(t *testing.T, state, operation string) {
	t.Helper()
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	tuple := pairingRecoveryTuple(t, repo, state)
	before, err := repo.Queries.GetAgentPairing(
		t.Context(),
		recoveryAgentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(),
		"CREATE TRIGGER pairing_error BEFORE "+operation+
			" BEGIN SELECT RAISE(ABORT, 'pairing-database-error'); END"); err != nil {
		t.Fatal(err)
	}
	_, err = repo.PrepareAgentPairing(t.Context(), tuple)
	if err == nil ||
		!strings.Contains(err.Error(), "pairing-database-error") ||
		errors.Is(err, ErrPairingTupleConflict) {
		t.Fatalf("database error was lost: %v", err)
	}
	after, err := repo.Queries.GetAgentPairing(
		t.Context(),
		recoveryAgentID,
	)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed recovery changed pairing: %v", err)
	}
}
