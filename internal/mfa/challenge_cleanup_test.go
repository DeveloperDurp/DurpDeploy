package mfa

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

type challengeDeleteCounter struct {
	*sql.DB
	cleanups int
}

func TestChallenge_MaintenanceCleansWithoutLoginTraffic(t *testing.T) {
	service, binding, conn := newChallengeServiceForTest(t)
	_, err := service.Issue(t.Context(), ChallengeIssue{
		UserID: binding.UserID, SessionID: binding.SessionID,
		Purpose: ChallengePurposeAdminMFAReset,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return time.Unix(1_700_000_301, 0) }
	// Expiry is enforced without requiring a global delete during resolution.
	if _, err := service.ResolveBound(t.Context(), binding); !errors.Is(
		err,
		ErrChallengeInvalid,
	) {
		t.Fatalf("expired resolve = %v", err)
	}
	if err := service.RecordFailure(t.Context(), binding); !errors.Is(
		err,
		ErrChallengeInvalid,
	) {
		t.Fatalf("expired failure = %v", err)
	}
	if err := service.CleanupExpired(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM mfa_challenges WHERE purpose != 'admin_mfa_reset'").Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("ordinary challenges = %d, error = %v", count, err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM mfa_challenges WHERE purpose = 'admin_mfa_reset'").Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("reset continuations = %d, error = %v", count, err)
	}
}

func (counter *challengeDeleteCounter) ExecContext(
	ctx context.Context, query string, args ...any,
) (sql.Result, error) {
	if strings.HasPrefix(query, "-- name: DeleteExpiredMFAChallenges") {
		counter.cleanups++
	}
	return counter.DB.ExecContext(ctx, query, args...)
}

func TestChallenge_FailedFactorCleansExpiredRowsOnce(t *testing.T) {
	// Given: the real database with a counter for global cleanup writes.
	service, binding, conn := newChallengeServiceForTest(t)
	counter := &challengeDeleteCounter{DB: conn}
	service.repo.Queries = db.New(counter)
	invalidFactor := errors.New("invalid factor")

	// When: a factor resolves, fails consumption, records failure, and rerenders.
	if _, err := service.ResolveBound(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	err := service.ConsumeWith(t.Context(), binding,
		func(context.Context, *db.Queries, db.MfaChallenge) error {
			return invalidFactor
		})
	if !errors.Is(err, invalidFactor) {
		t.Fatalf("factor result = %v", err)
	}
	if err := service.RecordFailure(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveBound(t.Context(), binding); err != nil {
		t.Fatal(err)
	}

	// Then: the failed attempt performs one global cleanup, not four.
	if counter.cleanups != 1 {
		t.Fatalf("global cleanup writes = %d, want 1", counter.cleanups)
	}
	challenge, err := service.activeChallenge(
		t.Context(),
		service.repo.Queries,
		binding,
	)
	if err != nil || challenge.Attempts != 1 {
		t.Fatalf("attempts=%d error=%v", challenge.Attempts, err)
	}
}
