package repository_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRemoteStepLockSerializesTerminalTransition(t *testing.T) {
	repo := remoteFixture(t)
	tokenHash := bytes.Repeat([]byte{1}, 32)
	if _, err := repo.DB.Exec(`INSERT INTO remote_step_runs
		(deployment_id,step_index,agent_id,state,claim_token_hash,ciphertext,
		 claim_expires_at,last_heartbeat_at,started_at,updated_at)
		VALUES (1,0,'a','started',?,'ciphertext',200,100,100,100)`,
		tokenHash,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := repo.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	locked, err := repo.Queries.WithTx(tx).LockRemoteStepRun(
		t.Context(),
		db.LockRemoteStepRunParams{
			DeploymentID: 1, AgentID: "a", ClaimTokenHash: tokenHash,
		},
	)
	if err != nil || locked != 1 {
		t.Fatalf("lock rows=%d error=%v", locked, err)
	}

	type result struct {
		handled bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		handled, err := repo.FinishRemoteStep(
			t.Context(),
			repository.RemoteLifecycleClaim{
				DeploymentID: 1, AgentID: "a", ClaimTokenHash: tokenHash,
			},
			"succeeded",
		)
		done <- result{handled: handled, err: err}
	}()
	select {
	case got := <-done:
		t.Fatalf("terminal transition bypassed row lock: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || !got.handled {
			t.Fatalf("terminal transition after unlock: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal transition remained blocked after commit")
	}
}

func TestRemoteStepStartRequiresLiveEligibleClaim(t *testing.T) {
	runRemoteStepStartEligibility(t, remoteFixture(t))
}

func runRemoteStepStartEligibility(
	t *testing.T,
	repo *repository.Repository,
) {
	t.Helper()
	for _, scenario := range []struct {
		name      string
		now       int64
		mutateSQL string
		wantRows  int64
	}{
		{name: "immediately before expiration", now: 109, wantRows: 1},
		{name: "at expiration", now: 110, wantRows: 0},
		{name: "after expiration", now: 111, wantRows: 0},
		{
			name:      "disabled agent",
			now:       109,
			mutateSQL: "UPDATE agents SET status = 'disabled' WHERE id = 'a'",
			wantRows:  0,
		},
		{
			name: "revoked agent",
			now:  109,
			mutateSQL: "UPDATE agents SET status = 'revoked', " +
				"revoked_at = 100 WHERE id = 'a'",
			wantRows: 0,
		},
		{
			name:      "unpaired agent",
			now:       109,
			mutateSQL: "DELETE FROM agent_pairings WHERE agent_id = 'a'",
			wantRows:  0,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			tx, err := repo.DB.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			queries := repo.Queries.WithTx(tx)
			tokenHash := bytes.Repeat([]byte{1}, 32)
			_, err = tx.Exec(`INSERT INTO remote_step_runs
				(deployment_id, step_index, agent_id, state, claim_token_hash,
				 ciphertext, claim_expires_at, last_heartbeat_at, updated_at)
				VALUES (1, 0, ?, 'claimed', ?, 'ciphertext', 110, 100, 100)`,
				"a", tokenHash)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.mutateSQL != "" {
				if _, err := tx.Exec(scenario.mutateSQL); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := queries.StartRemoteStepRun(
				context.Background(),
				db.StartRemoteStepRunParams{
					Now:            ni(scenario.now),
					DeploymentID:   1,
					StepIndex:      0,
					AgentID:        "a",
					ClaimTokenHash: tokenHash,
				},
			)
			if err != nil || changed != scenario.wantRows {
				t.Fatalf(
					"start rows=%d want=%d error=%v",
					changed,
					scenario.wantRows,
					err,
				)
			}
		})
	}
}
