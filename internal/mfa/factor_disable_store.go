package mfa

import (
	"context"
	"errors"

	"durpdeploy/internal/db"
)

func (s *FactorStore) Disable(ctx context.Context, userID int64) error {
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := s.lockUser(ctx, queries, userID); err != nil {
			return err
		}
		factors, err := queries.CountMFAFactors(ctx, userID)
		if err != nil {
			return err
		}
		if factors == 0 {
			return ErrMFAFactorRequired
		}
		return s.resetWith(ctx, queries, userID)
	})
	if errors.Is(err, ErrMFAFactorRequired) {
		return ErrMFAFactorRequired
	}
	if err != nil {
		return ErrMFAFactorOperation
	}
	return nil
}

func (s *FactorStore) Reset(ctx context.Context, userID int64) error {
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		return s.ResetWith(ctx, queries, userID)
	})
	if err != nil {
		return ErrMFAFactorOperation
	}
	return nil
}

func (s *FactorStore) ResetWith(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
) error {
	if err := s.lockUser(ctx, queries, userID); err != nil {
		return err
	}
	return s.resetWith(ctx, queries, userID)
}

func (s *FactorStore) resetWith(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
) error {
	if _, err := queries.DeleteTOTP(ctx, userID); err != nil {
		return err
	}
	if err := queries.DeleteWebAuthnCredentialsByUserID(
		ctx,
		userID,
	); err != nil {
		return err
	}
	if err := queries.DeleteRecoveryCodesByUserID(ctx, userID); err != nil {
		return err
	}
	if err := queries.DeleteMFARateLimit(ctx, userID); err != nil {
		return err
	}
	return s.invalidateBrowserAuth(ctx, queries, userID)
}
