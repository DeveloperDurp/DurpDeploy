package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

type sqliteCodeError int

func (err sqliteCodeError) Error() string { return "sqlite test error" }
func (err sqliteCodeError) Code() int     { return int(err) }

func TestSQLiteBusyClassificationRetriesOnlyPrimaryCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		busy bool
	}{
		{name: "primary busy", err: sqliteCodeError(5), busy: true},
		{name: "locked", err: sqliteCodeError(6), busy: false},
		{name: "extended busy", err: sqliteCodeError(5 | 1<<8), busy: false},
		{name: "arbitrary", err: errors.New("database is locked"), busy: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsSQLiteBusy(test.err); got != test.busy {
				t.Fatalf("IsSQLiteBusy()=%v want=%v", got, test.busy)
			}
		})
	}
}

func TestSQLiteBusyRetryValueReturnsOnlySuccessfulAttempt(t *testing.T) {
	// Given
	attempts := 0

	// When
	result, err := withSQLiteBusyRetryValue(
		t.Context(),
		func() (string, error) {
			attempts++
			if attempts == 1 {
				return "stale rolled-back claim", sqliteCodeError(
					sqliteBusyCode,
				)
			}
			return "", nil
		},
	)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || result != "" {
		t.Fatalf(
			"attempts=%d result=%q, want successful empty result",
			attempts,
			result,
		)
	}
}

func TestClaimRemoteDeploymentRetriesSQLiteBusyAtomically(t *testing.T) {
	engine := newDeploymentCreationEngine(t, "SQLite")
	engine.dsn = strings.Replace(
		engine.dsn,
		"busy_timeout(5000)",
		"busy_timeout(0)",
		1,
	)
	claimer, locker := openDeploymentCreationEngine(t, engine)
	created := createLegacyRemoteDeployment(t, claimer)
	lock := holdClaimWriter(t, locker.DB)
	started := make(chan struct{})
	type claimResult struct {
		claim   RemoteClaim
		claimed bool
		err     error
	}
	result := make(chan claimResult, 1)
	prepareCalls := 0
	go func() {
		close(started)
		claim, claimed, claimErr := claimer.ClaimRemoteDeploymentPayload(
			context.Background(),
			"race-agent",
			func(RemotePayloadSnapshot) (RemotePreparedClaim, error) {
				prepareCalls++
				return RemotePreparedClaim{
					Token:      "one-token",
					TokenHash:  bytes.Repeat([]byte{1}, 32),
					Ciphertext: []byte("sealed"),
				}, nil
			},
		)
		result <- claimResult{claim: claim, claimed: claimed, err: claimErr}
	}()
	<-started
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}

	got := <-result
	if got.err != nil || !got.claimed {
		t.Fatalf("claimed=%v error=%v", got.claimed, got.err)
	}
	if got.claim.DeploymentID != created.ID || prepareCalls != 1 {
		t.Fatalf(
			"deployment=%d prepare calls=%d",
			got.claim.DeploymentID,
			prepareCalls,
		)
	}
	var claimedRows int
	if err := claimer.DB.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM remote_deployment_claims WHERE state='claimed'",
	).Scan(&claimedRows); err != nil {
		t.Fatal(err)
	}
	if claimedRows != 1 {
		t.Fatalf("claimed rows=%d want=1", claimedRows)
	}
}

func holdClaimWriter(t *testing.T, locker *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := locker.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(
		t.Context(),
		"UPDATE agents SET updated_at=updated_at WHERE id='race-agent'",
	); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	return tx
}
