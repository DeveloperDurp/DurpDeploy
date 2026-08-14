package mfa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/db"
)

const (
	failureWindow    = 15 * time.Minute
	cooldownDuration = 15 * time.Minute
)

var ErrMFACooldown = errors.New("MFA temporarily unavailable")

type rateLimitState struct {
	userID int64
	now    int64
}

func rateLimitBlocked(
	ctx context.Context,
	queries *db.Queries,
	state rateLimitState,
) (bool, error) {
	limit, err := queries.GetMFARateLimitByUserID(ctx, state.userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get MFA rate limit: %w", err)
	}
	return limit.BlockedUntil.Valid && limit.BlockedUntil.Int64 > state.now, nil
}

func recordRateFailure(
	ctx context.Context,
	queries *db.Queries,
	state rateLimitState,
) error {
	limit, err := queries.GetMFARateLimitByUserID(ctx, state.userID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = queries.CreateMFARateLimit(ctx, db.CreateMFARateLimitParams{
			UserID: state.userID, WindowStartedAt: state.now, FailureCount: 1,
		})
		if err != nil {
			return fmt.Errorf("create MFA rate limit: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get MFA rate limit: %w", err)
	}
	windowStart := limit.WindowStartedAt
	failures := limit.FailureCount + 1
	if state.now-windowStart >= int64(failureWindow/time.Second) ||
		(limit.BlockedUntil.Valid && limit.BlockedUntil.Int64 <= state.now) {
		windowStart = state.now
		failures = 1
	}
	blockedUntil := sql.NullInt64{}
	if failures >= maxFailures {
		blockedUntil = sql.NullInt64{
			Int64: state.now + int64(cooldownDuration/time.Second), Valid: true,
		}
	}
	_, err = queries.UpdateMFARateLimit(ctx, db.UpdateMFARateLimitParams{
		UserID: state.userID, WindowStartedAt: windowStart, FailureCount: failures,
		BlockedUntil: blockedUntil,
	})
	if err != nil {
		return fmt.Errorf("update MFA rate limit: %w", err)
	}
	return nil
}
