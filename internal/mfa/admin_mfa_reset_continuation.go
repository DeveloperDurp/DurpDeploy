package mfa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/db"
)

func (s *ChallengeService) ReplaceAdminMFAResetContinuation(
	ctx context.Context,
	userID int64,
	sessionID string,
	ceremonyJSON string,
) error {
	if userID <= 0 || sessionID == "" || ceremonyJSON == "" {
		return ErrChallengeInvalid
	}
	token, err := randomChallengeValue(s.random)
	if err != nil {
		return fmt.Errorf("generate MFA reset continuation guard: %w", err)
	}
	csrf, err := randomChallengeValue(s.random)
	if err != nil {
		return fmt.Errorf("generate MFA reset continuation CSRF: %w", err)
	}
	now := s.clock().Unix()
	return s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := cleanExpiredChallenges(ctx, queries, now); err != nil {
			return err
		}
		if _, err := queries.DeleteMFAChallengesByUserSessionPurpose(
			ctx,
			db.DeleteMFAChallengesByUserSessionPurposeParams{
				UserID:    userID,
				SessionID: sql.NullString{String: sessionID, Valid: true},
				Purpose:   string(ChallengePurposeAdminMFAReset),
			},
		); err != nil {
			return fmt.Errorf("replace MFA reset continuation: %w", err)
		}
		if _, err := queries.CreateMFAChallenge(
			ctx,
			db.CreateMFAChallengeParams{
				TokenHash:    challengeHash(token),
				UserID:       userID,
				SessionID:    sql.NullString{String: sessionID, Valid: true},
				Purpose:      string(ChallengePurposeAdminMFAReset),
				CsrfHash:     challengeHash(csrf),
				CeremonyJson: ceremonyJSON,
				ExpiresAt:    now + int64(challengeTTL/time.Second),
			},
		); err != nil {
			return fmt.Errorf("create MFA reset continuation: %w", err)
		}
		return nil
	})
}

func (s *ChallengeService) ConsumeAdminMFAResetContinuation(
	ctx context.Context,
	userID int64,
	sessionID string,
	resume func(context.Context, *db.Queries, string) error,
) (found bool, err error) {
	if userID <= 0 || sessionID == "" {
		return false, ErrChallengeInvalid
	}
	now := s.clock().Unix()
	err = s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := lockMFAUser(ctx, queries, userID); err != nil {
			return err
		}
		challenge, err := queries.GetMFAChallengeByUserSessionPurpose(
			ctx,
			db.GetMFAChallengeByUserSessionPurposeParams{
				UserID:    userID,
				SessionID: sql.NullString{String: sessionID, Valid: true},
				Purpose:   string(ChallengePurposeAdminMFAReset),
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load MFA reset continuation: %w", err)
		}
		found = true
		if challenge.ExpiresAt <= now {
			if _, err := queries.ConsumeMFAChallenge(
				ctx,
				challenge.TokenHash,
			); err != nil {
				return fmt.Errorf(
					"discard expired MFA reset continuation: %w",
					err,
				)
			}
			return nil
		}
		consumed, err := queries.ConsumeMFAChallengeBySessionGuarded(
			ctx,
			db.ConsumeMFAChallengeBySessionGuardedParams{
				TokenHash: challenge.TokenHash,
				UserID:    userID,
				Purpose:   string(ChallengePurposeAdminMFAReset),
				SessionID: sql.NullString{String: sessionID, Valid: true},
				ExpiresAt: now,
			},
		)
		if err != nil {
			return fmt.Errorf("consume MFA reset continuation: %w", err)
		}
		if consumed != 1 {
			return ErrChallengeInvalid
		}
		return resume(ctx, queries, challenge.CeremonyJson)
	})
	return found, err
}
