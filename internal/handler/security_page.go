package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/views/pages"
)

func (h *AuthHandler) SecurityGet(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	user, session, ok := h.securityIdentity(r)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	factorCount, err := h.repo.Queries.CountMFAFactors(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load MFA factors", http.StatusInternalServerError)
		return
	}
	hasTOTP, err := h.hasTOTP(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load authenticator", http.StatusInternalServerError)
		return
	}
	credentials, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		http.Error(w, "load passkeys", http.StatusInternalServerError)
		return
	}

	if err := pages.SecurityPage(
		credentials,
		hasTOTP,
		len(credentials) > 0,
		factorCount,
		auth.IsRecentlyAuthenticated(session),
		r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, "render security page", http.StatusInternalServerError)
	}
}

func (h *AuthHandler) hasTOTP(ctx context.Context, userID int64) (bool, error) {
	_, err := h.repo.Queries.GetTOTPByUserID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
