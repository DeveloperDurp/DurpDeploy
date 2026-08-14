package mfa

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

type storedTOTP struct {
	seed             string
	lastAcceptedStep int64
}

func (s *FactorStore) ActivateTOTP(
	ctx context.Context,
	userID int64,
	seed string,
	acceptedStep int64,
) ([]string, error) {
	if s.box == nil {
		return nil, ErrMFAFactorOperation
	}
	encryptedSeed, err := s.box.Encrypt(seed)
	if err != nil {
		return nil, ErrMFAFactorOperation
	}
	var codes []string
	err = s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := s.lockUser(ctx, queries, userID); err != nil {
			return err
		}
		codes, err = s.activateTOTPWith(
			ctx,
			queries,
			userID,
			encryptedSeed,
			acceptedStep,
		)
		return err
	})
	if err != nil {
		return nil, ErrMFAFactorOperation
	}
	return codes, nil
}

func (s *FactorStore) ActivateTOTPWith(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
	seed string,
	acceptedStep int64,
) ([]string, error) {
	if s.box == nil {
		return nil, ErrMFAFactorOperation
	}
	encryptedSeed, err := s.box.Encrypt(seed)
	if err != nil {
		return nil, ErrMFAFactorOperation
	}
	return s.activateTOTPWith(ctx, queries, userID, encryptedSeed, acceptedStep)
}

func (s *FactorStore) activateTOTPWith(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
	encryptedSeed string,
	acceptedStep int64,
) ([]string, error) {
	factors, err := queries.CountMFAFactors(ctx, userID)
	if err != nil {
		return nil, err
	}
	if _, err := queries.CreateTOTP(ctx, db.CreateTOTPParams{
		UserID: userID, EncryptedSeed: []byte(encryptedSeed),
		LastAcceptedStep: sql.NullInt64{Int64: acceptedStep, Valid: true},
	}); err != nil {
		return nil, err
	}
	var codes []string
	if factors == 0 {
		codes, err = s.replaceRecoveryCodes(ctx, queries, userID)
	}
	if err != nil {
		return nil, err
	}
	if err := s.invalidateBrowserAuth(ctx, queries, userID); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *FactorStore) VerifyTOTP(
	ctx context.Context,
	userID int64,
	code string,
) error {
	return s.repo.WithTx(ctx, func(queries *db.Queries) error {
		return s.VerifyTOTPWith(ctx, queries, userID, code)
	})
}

func (s *FactorStore) VerifyTOTPWith(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
	code string,
) error {
	factor, err := s.loadTOTPWithQueries(ctx, queries, userID)
	if err != nil {
		return ErrMFAFactorOperation
	}
	step, err := s.totp.Verify(code, factor.seed, factor.lastAcceptedStep)
	if err != nil {
		return err
	}
	updated, err := queries.AdvanceTOTPIfNewer(
		ctx,
		db.AdvanceTOTPIfNewerParams{
			UserID: userID, NextStep: sql.NullInt64{Int64: step, Valid: true},
		},
	)
	if err != nil || updated != 1 {
		return ErrTOTPReplay
	}
	return nil
}

func (s *FactorStore) loadTOTP(
	ctx context.Context,
	userID int64,
) (storedTOTP, error) {
	return s.loadTOTPWithQueries(ctx, s.repo.Queries, userID)
}

func (s *FactorStore) loadTOTPWithQueries(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
) (storedTOTP, error) {
	if s.box == nil {
		return storedTOTP{}, ErrMFAFactorOperation
	}
	factor, err := queries.GetTOTPByUserID(ctx, userID)
	if err != nil {
		return storedTOTP{}, ErrMFAFactorOperation
	}
	seed, err := s.box.Decrypt(string(factor.EncryptedSeed))
	if err != nil {
		return storedTOTP{}, ErrMFAFactorOperation
	}
	return storedTOTP{
		seed:             seed,
		lastAcceptedStep: factor.LastAcceptedStep.Int64,
	}, nil
}
