package mfa

import (
	"context"
	"database/sql"
	"errors"
	"io"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

var (
	ErrMFAFactorOperation = errors.New(
		"MFA factor operation failed",
	)
	ErrMFAFactorRequired = errors.New(
		"at least one MFA factor is required",
	)
	ErrMFAFactorCredentialInvalid     = errors.New("invalid MFA credential")
	ErrMFAFactorCredentialUnavailable = errors.New("MFA credential unavailable")
)

type FactorStore struct {
	repo           *repository.Repository
	box            *secret.Box
	totp           TOTP
	recoveryRandom io.Reader
}

type FactorStoreConfig struct {
	Repository     *repository.Repository
	Box            *secret.Box
	TOTP           TOTP
	RecoveryRandom io.Reader
}

type CredentialActivation struct {
	Credential    db.WebauthnCredential
	RecoveryCodes []string
}

type factorDeletion struct {
	verify            func(*db.Queries) error
	delete            func(*db.Queries) (int64, error)
	retainBrowserAuth bool
}

func NewFactorStore(config FactorStoreConfig) *FactorStore {
	return &FactorStore{
		repo:           config.Repository,
		box:            config.Box,
		totp:           config.TOTP,
		recoveryRandom: config.RecoveryRandom,
	}
}

func (s *FactorStore) ConsumeRecovery(
	ctx context.Context,
	userID int64,
	code string,
	usedAt int64,
) error {
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		return s.ConsumeRecoveryWith(ctx, queries, userID, code, usedAt)
	})
	if err != nil {
		return ErrInvalidRecoveryCode
	}
	return nil
}

func (s *FactorStore) ConsumeRecoveryWith(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
	code string,
	usedAt int64,
) error {
	hash, err := HashRecoveryCode(code)
	if err != nil {
		return ErrInvalidRecoveryCode
	}
	_, err = queries.ConsumeRecoveryCode(
		ctx,
		db.ConsumeRecoveryCodeParams{
			UserID: userID, CodeHash: hash[:],
			UsedAt: sql.NullInt64{Int64: usedAt, Valid: true},
		},
	)
	if err != nil {
		return ErrInvalidRecoveryCode
	}
	return nil
}

func (s *FactorStore) Enabled(ctx context.Context, userID int64) (bool, error) {
	factors, err := s.repo.Queries.CountMFAFactors(ctx, userID)
	if err != nil {
		return false, ErrMFAFactorOperation
	}
	return factors > 0, nil
}

func (s *FactorStore) deleteFactor(
	ctx context.Context,
	userID int64,
	deletion factorDeletion,
) error {
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := s.lockUser(ctx, queries, userID); err != nil {
			return err
		}
		if deletion.verify != nil {
			if err := deletion.verify(queries); err != nil {
				return err
			}
		}
		factors, err := queries.CountMFAFactors(ctx, userID)
		if err != nil {
			return err
		}
		if factors <= 1 {
			return ErrMFAFactorRequired
		}
		deleted, err := deletion.delete(queries)
		if err != nil || deleted != 1 {
			return ErrMFAFactorOperation
		}
		if deletion.retainBrowserAuth {
			return nil
		}
		return s.invalidateBrowserAuth(ctx, queries, userID)
	})
	if err != nil {
		if errors.Is(err, ErrMFAFactorRequired) {
			return ErrMFAFactorRequired
		}
		if errors.Is(err, ErrMFAFactorCredentialUnavailable) {
			return ErrMFAFactorCredentialUnavailable
		}
		return ErrMFAFactorOperation
	}
	return nil
}

func (s *FactorStore) lockUser(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
) error {
	locked, err := queries.LockMFAUser(ctx, userID)
	if err != nil || locked != 1 {
		return ErrMFAFactorOperation
	}
	return nil
}

func (s *FactorStore) invalidateBrowserAuth(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
) error {
	return auth.InvalidateBrowserAuthInTx(ctx, queries, userID)
}
