package handler

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/views/pages"
)

func (h *AuthHandler) loginMFAAvailability(
	ctx context.Context,
	userID int64,
) (pages.LoginMFAAvailability, error) {
	availability := pages.LoginMFAAvailability{}
	if _, err := h.repo.Queries.GetTOTPByUserID(ctx, userID); err == nil {
		availability.TOTP = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return pages.LoginMFAAvailability{}, err
	}

	credentials, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		ctx,
		userID,
	)
	if err != nil {
		return pages.LoginMFAAvailability{}, err
	}
	availability.Passkey = len(credentials) > 0

	recoveryCodes, err := h.repo.Queries.ListUnusedRecoveryCodesByUserID(
		ctx,
		userID,
	)
	if err != nil {
		return pages.LoginMFAAvailability{}, err
	}
	availability.Recovery = len(recoveryCodes) > 0
	return availability, nil
}
