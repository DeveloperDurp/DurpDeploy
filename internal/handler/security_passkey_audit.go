package handler

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/requestmeta"
)

// Record only after the bound challenge and credential transaction commits.
func (h *AuthHandler) auditPasskeyCloneWarning(
	r *http.Request, credential db.WebauthnCredential, purpose string,
) {
	if credential.CloneWarning != 1 {
		return
	}
	details, _ := json.Marshal(map[string]any{
		"credential_id": base64.RawURLEncoding.EncodeToString(
			credential.CredentialID,
		),
		"purpose":    purpose,
		"ip":         requestmeta.ClientIP(r),
		"user_agent": r.UserAgent(),
		"path":       r.URL.Path,
		"method":     r.Method,
	})
	audit.Record(r.Context(), h.repo, audit.Entry{
		UserID:     sql.NullInt64{Int64: credential.UserID, Valid: true},
		Action:     "mfa_passkey_clone_warning",
		EntityType: "user",
		EntityID:   sql.NullInt64{Int64: credential.UserID, Valid: true},
		Details:    string(details),
	})
}
