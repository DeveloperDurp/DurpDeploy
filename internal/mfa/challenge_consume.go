package mfa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

// Consume invokes success only after its guarded one-use delete commits. A
// success callback error is returned without restoring the consumed challenge.
func (s *ChallengeService) Consume(
	ctx context.Context,
	binding ChallengeBinding,
	success func(context.Context, db.MfaChallenge) error,
) error {
	var challenge db.MfaChallenge
	if err := s.ConsumeWith(
		ctx,
		binding,
		func(
			_ context.Context,
			_ *db.Queries,
			consumed db.MfaChallenge,
		) error {
			challenge = consumed
			return nil
		},
	); err != nil {
		return err
	}
	if err := success(ctx, challenge); err != nil {
		return fmt.Errorf("elevate consumed MFA challenge: %w", err)
	}
	return nil
}

// ConsumeWith commits a factor mutation and guarded challenge consumption in
// one transaction. A lost or invalid challenge rolls back the mutation.
func (s *ChallengeService) ConsumeWith(
	ctx context.Context,
	binding ChallengeBinding,
	success func(context.Context, *db.Queries, db.MfaChallenge) error,
) error {
	now := s.clock().Unix()
	if err := cleanExpiredChallenges(ctx, s.repo.Queries, now); err != nil {
		return err
	}
	var challenge db.MfaChallenge
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := lockMFAUser(ctx, queries, binding.UserID); err != nil {
			return err
		}
		blocked, err := rateLimitBlocked(ctx, queries, rateLimitState{
			userID: binding.UserID, now: now,
		})
		if err != nil {
			return err
		}
		if blocked {
			return ErrMFACooldown
		}
		challenge, err = queries.GetActiveMFAChallenge(ctx,
			db.GetActiveMFAChallengeParams{
				TokenHash: challengeHash(binding.Token), ExpiresAt: now,
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrChallengeInvalid
		}
		if err != nil {
			return fmt.Errorf("get MFA challenge for elevation: %w", err)
		}
		consumed, err := queries.ConsumeMFAChallengeGuarded(ctx,
			db.ConsumeMFAChallengeGuardedParams{
				TokenHash: challengeHash(binding.Token), UserID: binding.UserID,
				Purpose: string(
					binding.Purpose,
				), SessionID: challengeSession(binding.SessionID),
				CsrfHash: challengeHash(
					binding.CSRF,
				), ExpiresAt: now, Attempts: maxFailures,
			},
		)
		if err != nil {
			return fmt.Errorf("guarded consume MFA challenge: %w", err)
		}
		if consumed != 1 {
			return ErrChallengeInvalid
		}
		if err := success(ctx, queries, challenge); err != nil {
			return err
		}
		if err := queries.DeleteMFARateLimit(ctx, binding.UserID); err != nil {
			return fmt.Errorf("clear MFA rate limit: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *ChallengeService) RecordFailure(
	ctx context.Context,
	binding ChallengeBinding,
) error {
	now := s.clock().Unix()
	if err := cleanExpiredChallenges(ctx, s.repo.Queries, now); err != nil {
		return err
	}
	return s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := lockMFAUser(ctx, queries, binding.UserID); err != nil {
			return err
		}
		challenge, err := s.activeChallenge(ctx, queries, binding)
		if err != nil {
			return err
		}
		blocked, err := rateLimitBlocked(ctx, queries, rateLimitState{
			userID: challenge.UserID, now: now,
		})
		if err != nil {
			return err
		}
		if blocked {
			return ErrMFACooldown
		}
		_, err = queries.IncrementMFAChallengeAttempts(ctx,
			db.IncrementMFAChallengeAttemptsParams{
				TokenHash: challengeHash(binding.Token), Attempts: maxFailures,
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrChallengeInvalid
		}
		if err != nil {
			return fmt.Errorf("increment MFA challenge attempts: %w", err)
		}
		return recordRateFailure(ctx, queries, rateLimitState{
			userID: challenge.UserID, now: now,
		})
	})
}

func lockMFAUser(ctx context.Context, queries *db.Queries, userID int64) error {
	locked, err := queries.LockMFAUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("lock MFA user: %w", err)
	}
	if locked != 1 {
		return ErrChallengeInvalid
	}
	return nil
}

func (s *ChallengeService) activeChallenge(
	ctx context.Context,
	queries *db.Queries,
	binding ChallengeBinding,
) (db.MfaChallenge, error) {
	now := s.clock().Unix()
	challenge, err := queries.GetActiveMFAChallenge(
		ctx,
		db.GetActiveMFAChallengeParams{
			TokenHash: challengeHash(binding.Token), ExpiresAt: now,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.MfaChallenge{}, ErrChallengeInvalid
	}
	if err != nil {
		return db.MfaChallenge{}, fmt.Errorf("get MFA challenge: %w", err)
	}
	if challenge.UserID != binding.UserID ||
		challenge.Purpose != string(binding.Purpose) ||
		challenge.SessionID != challengeSession(binding.SessionID) ||
		challenge.Attempts >= maxFailures ||
		!equalChallengeHash(challenge.CsrfHash, challengeHash(binding.CSRF)) {
		return db.MfaChallenge{}, ErrChallengeInvalid
	}
	return challenge, nil
}
