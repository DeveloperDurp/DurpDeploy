package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

const secretExportPrefix = "/admin/secret-exports/"

type SecretExportHandler struct{ repo *repository.Repository }

func NewSecretExportHandler(repo *repository.Repository) *SecretExportHandler {
	return &SecretExportHandler{repo: repo}
}

func secretExportError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	message := "Unable to export secrets"
	switch {
	case errors.Is(err, repository.ErrSecretExportInvalid),
		errors.Is(err, sql.ErrNoRows):
		status, message = http.StatusNotFound, "Export not found, expired, or not approved"
	case errors.Is(err, repository.ErrSecretExportScope):
		status, message = http.StatusBadRequest, err.Error()
	}
	auth.RenderRequestError(w, r, status, message)
}

// API exports require a global token whose effective role remains admin.
func secretExportIdentity(
	w http.ResponseWriter,
	r *http.Request,
	api bool,
) (*db.User, bool) {
	setNoStore(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Role != "admin" ||
		auth.RoleFromContext(r.Context()) != "admin" {
		auth.RenderRequestError(
			w,
			r,
			http.StatusForbidden,
			"Instance administrator permission is required",
		)
		return nil, false
	}
	if api {
		token := auth.TokenFromContext(r.Context())
		if token == nil || token.Scope != "global" {
			auth.RenderRequestError(
				w,
				r,
				http.StatusForbidden,
				"A global administrator token is required",
			)
			return nil, false
		}
	} else if auth.SessionFromContext(r.Context()) == nil {
		auth.RenderRequestError(w, r, http.StatusUnauthorized, "Browser session required")
		return nil, false
	}
	return user, true
}

func (h *SecretExportHandler) Start(w http.ResponseWriter, r *http.Request) {
	user, ok := secretExportIdentity(w, r, false)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid export form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PostFormValue("scope_id"), 10, 64)
	if err != nil {
		secretExportError(w, r, repository.ErrSecretExportScope)
		return
	}
	row, _, err := h.repo.NewSecretExport(
		r.Context(),
		db.CreateSecretExportParams{
			UserID: user.ID, SessionID: auth.SessionFromContext(r.Context()).ID,
			Scope: r.PostFormValue("scope"), ScopeID: id,
		},
	)
	if err != nil {
		secretExportError(w, r, err)
		return
	}
	http.Redirect(w, r, secretExportPrefix+row.ID, http.StatusSeeOther)
}

func (h *SecretExportHandler) APIStart(w http.ResponseWriter, r *http.Request) {
	user, ok := secretExportIdentity(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Scope   string `json:"scope"`
		ScopeID int64  `json:"scope_id"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	row, claim, err := h.repo.NewSecretExport(
		r.Context(),
		db.CreateSecretExportParams{
			UserID: user.ID, TokenID: auth.TokenFromContext(r.Context()).ID,
			Scope: input.Scope, ScopeID: input.ScopeID,
		},
	)
	if err != nil {
		secretExportError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": row.ID, "approval_url": secretExportPrefix + row.ID,
		"claim_token": claim, "expires_at": row.ExpiresAt,
	})
}

func (h *SecretExportHandler) Show(w http.ResponseWriter, r *http.Request) {
	user, ok := secretExportIdentity(w, r, false)
	if !ok {
		return
	}
	row, err := h.repo.SecretExport(
		r.Context(),
		chi.URLParam(r, "exportId"),
		user.ID,
	)
	if err != nil {
		secretExportError(w, r, err)
		return
	}
	if row.SessionID != "" &&
		row.SessionID != auth.SessionFromContext(r.Context()).ID {
		secretExportError(w, r, repository.ErrSecretExportInvalid)
		return
	}
	name, err := h.repo.SecretExportScopeName(
		r.Context(),
		row.Scope,
		row.ScopeID,
	)
	if err != nil {
		secretExportError(w, r, err)
		return
	}
	if err := pages.SecretExportPage(row, name, auth.SessionFromContext(r.Context()).CsrfToken).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Render export confirmation",
			http.StatusInternalServerError,
		)
	}
}

func (h *SecretExportHandler) Authorize(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, ok := secretExportIdentity(w, r, false)
	if !ok {
		return
	}
	id := chi.URLParam(r, "exportId")
	session := auth.SessionFromContext(r.Context())
	row, err := h.repo.SecretExport(r.Context(), id, user.ID)
	if err != nil {
		secretExportError(w, r, err)
		return
	}
	if row.Approved != 0 {
		secretExportError(w, r, repository.ErrSecretExportInvalid)
		return
	}
	if row.SessionID == "" {
		count, err := h.repo.Queries.BindSecretExport(
			r.Context(),
			db.BindSecretExportParams{
				ID: id, UserID: user.ID, SessionID: session.ID, ExpiresAt: time.Now().Unix(),
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
	} else if row.SessionID != session.ID {
		secretExportError(w, r, repository.ErrSecretExportInvalid)
		return
	}
	http.Redirect(
		w,
		r,
		"/settings/security/reauth?export_id="+id,
		http.StatusSeeOther,
	)
}

func (h *SecretExportHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	user, ok := secretExportIdentity(w, r, false)
	if !ok {
		return
	}
	if err := h.repo.Queries.CancelSecretExport(r.Context(), db.CancelSecretExportParams{
		ID: chi.URLParam(r, "exportId"), UserID: user.ID,
	}); err != nil {
		secretExportError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

func (h *SecretExportHandler) Download(w http.ResponseWriter, r *http.Request) {
	user, ok := secretExportIdentity(w, r, false)
	if !ok {
		return
	}
	h.download(w, r, user.ID, auth.SessionFromContext(r.Context()).ID, "", "")
}

func (h *SecretExportHandler) APIDownload(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, ok := secretExportIdentity(w, r, true)
	if !ok {
		return
	}
	var input struct {
		ClaimToken string `json:"claim_token"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	h.download(
		w,
		r,
		user.ID,
		"",
		auth.TokenFromContext(r.Context()).ID,
		input.ClaimToken,
	)
}

func (h *SecretExportHandler) download(
	w http.ResponseWriter,
	r *http.Request,
	userID int64,
	sessionID, tokenID, claim string,
) {
	result, err := h.repo.TakeSecretExport(
		r.Context(),
		chi.URLParam(r, "exportId"),
		userID,
		sessionID,
		tokenID,
		claim,
	)
	if err != nil {
		secretExportError(w, r, err)
		return
	}
	audit.SetSecretExportScope(r, result.Scope, result.ScopeID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().
		Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%d-secrets.json"`, result.Scope, result.ScopeID))
	_ = json.NewEncoder(w).Encode(result)
}
