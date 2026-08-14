package mfa

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"durpdeploy/internal/db"
)

func (s *FactorStore) CreateCredential(
	ctx context.Context,
	credential db.CreateWebAuthnCredentialParams,
) (CredentialActivation, error) {
	var activation CredentialActivation
	var err error
	err = s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := s.lockUser(ctx, queries, credential.UserID); err != nil {
			return err
		}
		activation, err = s.CreateCredentialWith(ctx, queries, credential)
		return err
	})
	if err != nil {
		return CredentialActivation{}, ErrMFAFactorOperation
	}
	return activation, nil
}

func (s *FactorStore) CreateCredentialWith(
	ctx context.Context,
	queries *db.Queries,
	credential db.CreateWebAuthnCredentialParams,
) (CredentialActivation, error) {
	factors, err := queries.CountMFAFactors(ctx, credential.UserID)
	if err != nil {
		return CredentialActivation{}, err
	}
	created, err := queries.CreateWebAuthnCredential(ctx, credential)
	if err != nil {
		return CredentialActivation{}, err
	}
	activation := CredentialActivation{Credential: created}
	if factors == 0 {
		activation.RecoveryCodes, err = s.replaceRecoveryCodes(
			ctx,
			queries,
			credential.UserID,
		)
	}
	if err != nil {
		return CredentialActivation{}, err
	}
	if err := s.invalidateBrowserAuth(
		ctx,
		queries,
		credential.UserID,
	); err != nil {
		return CredentialActivation{}, err
	}
	return activation, nil
}

func (s *FactorStore) LoadCredential(
	ctx context.Context,
	credentialID []byte,
) (db.WebauthnCredential, error) {
	credential, err := s.repo.Queries.GetWebAuthnCredentialByID(
		ctx,
		credentialID,
	)
	if err != nil {
		return db.WebauthnCredential{}, ErrMFAFactorOperation
	}
	return credential, nil
}

func (s *FactorStore) UpdateCredentialCounter(
	ctx context.Context,
	credentialID []byte,
	signCount int64,
	cloneWarning int64,
) (db.WebauthnCredential, error) {
	var credential db.WebauthnCredential
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		var updateErr error
		credential, updateErr = s.UpdateCredentialCounterWith(
			ctx,
			queries,
			credentialID,
			signCount,
			cloneWarning,
		)
		return updateErr
	})
	if err != nil {
		return db.WebauthnCredential{}, ErrMFAFactorOperation
	}
	return credential, nil
}

func (s *FactorStore) UpdateCredentialCounterWith(
	ctx context.Context,
	queries *db.Queries,
	credentialID []byte,
	signCount int64,
	cloneWarning int64,
) (db.WebauthnCredential, error) {
	credential, err := queries.UpdateWebAuthnCredentialCounter(
		ctx,
		db.UpdateWebAuthnCredentialCounterParams{
			CredentialID: credentialID,
			SignCount:    signCount,
			CloneWarning: cloneWarning,
		},
	)
	if err != nil {
		return db.WebauthnCredential{}, ErrMFAFactorOperation
	}
	return credential, nil
}

func (s *FactorStore) DeleteTOTP(ctx context.Context, userID int64) error {
	return s.deleteFactor(
		ctx,
		userID,
		factorDeletion{
			delete: func(queries *db.Queries) (int64, error) {
				return queries.DeleteTOTP(ctx, userID)
			},
		},
	)
}

func (s *FactorStore) DeleteCredential(
	ctx context.Context,
	userID int64,
	credentialID []byte,
) error {
	if len(credentialID) == 0 {
		return ErrMFAFactorCredentialInvalid
	}
	return s.deleteFactor(
		ctx,
		userID,
		factorDeletion{
			retainBrowserAuth: true,
			verify: func(queries *db.Queries) error {
				credential, err := queries.GetWebAuthnCredentialByID(
					ctx,
					credentialID,
				)
				if errors.Is(err, sql.ErrNoRows) {
					return ErrMFAFactorCredentialUnavailable
				}
				if err != nil {
					return err
				}
				if credential.UserID != userID {
					return ErrMFAFactorCredentialUnavailable
				}
				return nil
			},
			delete: func(queries *db.Queries) (int64, error) {
				return queries.DeleteWebAuthnCredentialByUserID(
					ctx,
					db.DeleteWebAuthnCredentialByUserIDParams{
						CredentialID: credentialID,
						UserID:       userID,
					},
				)
			},
		},
	)
}

func (s *FactorStore) RenameCredential(
	ctx context.Context,
	userID int64,
	credentialID []byte,
	name string,
) error {
	if len(credentialID) == 0 || strings.TrimSpace(name) == "" {
		return ErrMFAFactorOperation
	}
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := s.lockUser(ctx, queries, userID); err != nil {
			return err
		}
		updated, err := queries.RenameWebAuthnCredentialByUserID(
			ctx,
			db.RenameWebAuthnCredentialByUserIDParams{
				Name:         strings.TrimSpace(name),
				CredentialID: credentialID,
				UserID:       userID,
			},
		)
		if err != nil || updated != 1 {
			return ErrMFAFactorOperation
		}
		return s.invalidateBrowserAuth(ctx, queries, userID)
	})
	if err != nil {
		return ErrMFAFactorOperation
	}
	return nil
}

func (s *FactorStore) EnsureWebAuthnIdentity(
	ctx context.Context,
	userID int64,
	rpID string,
) (db.WebauthnUser, error) {
	var identity db.WebauthnUser
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := s.lockUser(ctx, queries, userID); err != nil {
			return err
		}
		existing, err := queries.GetWebAuthnUserByUserID(ctx, userID)
		if err == nil {
			if existing.RpID != rpID {
				return ErrMFAFactorOperation
			}
			identity = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		params, err := NewWebAuthnUserIdentity(userID, rpID)
		if err != nil {
			return err
		}
		identity, err = queries.CreateWebAuthnUser(ctx, params)
		return err
	})
	if err != nil {
		return db.WebauthnUser{}, ErrMFAFactorOperation
	}
	return identity, nil
}
