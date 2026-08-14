package mfa

import (
	"context"
	"encoding/hex"
	"errors"

	"durpdeploy/internal/db"
)

func (s *FactorStore) RegenerateRecovery(
	ctx context.Context,
	userID int64,
) ([]string, error) {
	var codes []string
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
		var replaceErr error
		codes, replaceErr = s.replaceRecoveryCodes(ctx, queries, userID)
		if replaceErr != nil {
			return replaceErr
		}
		return s.invalidateBrowserAuth(ctx, queries, userID)
	})
	if err != nil {
		if errors.Is(err, ErrMFAFactorRequired) {
			return nil, ErrMFAFactorRequired
		}
		return nil, ErrMFAFactorOperation
	}
	return codes, nil
}

func (s *FactorStore) replaceRecoveryCodes(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
) ([]string, error) {
	codes, err := GenerateRecoveryCodes(s.recoveryRandom)
	if err != nil {
		return nil, err
	}
	if err := queries.DeleteRecoveryCodesByUserID(ctx, userID); err != nil {
		return nil, err
	}
	for _, code := range codes {
		hash, err := HashRecoveryCode(code)
		if err != nil {
			return nil, err
		}
		if _, err := queries.CreateRecoveryCode(
			ctx,
			db.CreateRecoveryCodeParams{
				ID: hex.EncodeToString(
					hash[:],
				), UserID: userID, CodeHash: hash[:],
			},
		); err != nil {
			return nil, err
		}
	}
	return codes, nil
}
