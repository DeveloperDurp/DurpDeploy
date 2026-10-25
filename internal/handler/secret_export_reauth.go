package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func (h *AuthHandler) validateSecretExport(r *http.Request, id string) error {
	user, session, ok := h.securityIdentity(r)
	if !ok || user.Role != "admin" {
		return repository.ErrSecretExportInvalid
	}
	row, err := h.repo.SecretExport(r.Context(), id, user.ID)
	if err != nil {
		return err
	}
	if row.SessionID != session.ID || row.Approved != 0 {
		return repository.ErrSecretExportInvalid
	}
	return nil
}

func (h *AuthHandler) approveSecretExport(
	w http.ResponseWriter,
	r *http.Request,
	id string,
) {
	user := auth.UserFromContext(r.Context())
	session := auth.SessionFromContext(r.Context())
	if err := h.validateSecretExport(r, id); err != nil {
		secretExportError(w, r, err)
		return
	}
	now := time.Now().Unix()
	count, err := h.repo.Queries.ApproveSecretExport(
		r.Context(),
		db.ApproveSecretExportParams{
			ID: id, UserID: user.ID, SessionID: session.ID, ExpiresAt: now, Now: now,
		},
	)
	if err != nil {
		secretExportError(w, r, err)
		return
	}
	if count != 1 {
		secretExportError(w, r, repository.ErrSecretExportInvalid)
		return
	}
	http.Redirect(w, r, secretExportPrefix+id, http.StatusSeeOther)
}

func (h *AuthHandler) bindSecretExportMFA(
	r *http.Request,
	id, token string,
) error {
	if id == "" {
		return nil
	}
	user, session, ok := h.securityIdentity(r)
	if !ok {
		return repository.ErrSecretExportInvalid
	}
	count, err := h.repo.Queries.SetSecretExportAuth(
		r.Context(),
		db.SetSecretExportAuthParams{
			ID: id, UserID: user.ID, SessionID: session.ID,
			AuthHash: repository.SecretExportHash(
				token,
			), ExpiresAt: time.Now().Unix(),
		},
	)
	if err != nil {
		return err
	}
	if count != 1 {
		return repository.ErrSecretExportInvalid
	}
	return nil
}

func (h *AuthHandler) secretExportForMFA(
	r *http.Request,
	token string,
) (string, error) {
	user, session, ok := h.securityIdentity(r)
	if !ok {
		return "", repository.ErrSecretExportInvalid
	}
	id, err := h.repo.Queries.SecretExportForAuth(
		r.Context(),
		db.SecretExportForAuthParams{
			UserID: user.ID, SessionID: session.ID, AuthHash: repository.SecretExportHash(token), ExpiresAt: time.Now().Unix(),
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
