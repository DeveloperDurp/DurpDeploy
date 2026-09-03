package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

// TokensHandler manages the web UI for API tokens: the self-service
// /settings/tokens page (a user's own tokens) and the admin
// /admin/tokens page (all tokens across all users).
//
// ponytail: the create path reuses auth.MintApiToken + the sqlc
// CreateApiToken query — same path the JSON API handler uses. No new
// token-minting helper; the handler is a thin web adapter over the
// existing primitives. The one-time plaintext token is displayed via
// a query string banner; replacing that legacy flow is tracked separately.
type TokensHandler struct {
	repo *repository.Repository
}

func NewTokensHandler(repo *repository.Repository) *TokensHandler {
	return &TokensHandler{repo: repo}
}

// MyTokens handles GET /settings/tokens. Renders the user's own
// tokens plus an optional one-time banner showing a freshly-created
// token's plaintext (carried via the new_token/new_name query params
// set by MyTokensPost).
func (h *TokensHandler) MyTokens(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	tokens, err := h.repo.Queries.ListApiTokensByUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	newToken := r.URL.Query().Get("new_token")
	newName := r.URL.Query().Get("new_name")

	if err := pages.TokensSettings(
		tokens,
		newToken,
		newName,
		"",
		r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// MyTokensPost handles POST /settings/tokens. Mints a new token for
// the current user and 303-redirects to /settings/tokens with the
// plaintext in the query string so the list page renders the one-time
// banner. The plaintext is gone on the next page load (the "Got it"
// button navigates back without the query).
func (h *TokensHandler) MyTokensPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.renderCreateError(w, r, "Name is required")
		return
	}

	full, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := h.repo.Queries.CreateApiToken(
		r.Context(),
		db.CreateApiTokenParams{
			ID:          uuid.NewString(),
			UserID:      user.ID,
			Name:        name,
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
			ExpiresAt:   sql.NullInt64{},
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirect := fmt.Sprintf(
		"/settings/tokens?new_token=%s&new_name=%s",
		url.QueryEscape(full),
		url.QueryEscape(name),
	)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirect)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// renderCreateError re-renders the settings page with a validation
// error alert. The form is inline on the list page (not a separate
// route), so the simplest correct path is a 422 + full-page re-render
// with the error message in an alert at the top.
//
// ponytail: the users handler uses a fragment swap because its form
// is a separate page; here the form lives on the list page so there's
// no fragment to swap to.
func (h *TokensHandler) renderCreateError(
	w http.ResponseWriter,
	r *http.Request,
	msg string,
) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	tokens, err := h.repo.Queries.ListApiTokensByUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := pages.TokensSettings(tokens, "", "", msg, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// MyTokensRevoke handles POST /settings/tokens/{id}/revoke. Revokes
// one of the current user's own tokens. A token that does not belong
// to the user (or is already revoked) is a 404 — we don't leak
// existence to other users' token IDs.
func (h *TokensHandler) MyTokensRevoke(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	id := chi.URLParam(r, "id")
	row, err := h.repo.Queries.GetApiTokenByID(r.Context(), id)
	if err != nil || row.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if err := h.repo.Queries.RevokeApiToken(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirect := "/settings/tokens"
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirect)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// AdminTokens handles GET /admin/tokens. Lists every token across
// all users. The /admin/* sub-group is gated by RequireRole("admin")
// in server.go, so the handler does not re-check the role.
func (h *TokensHandler) AdminTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.repo.Queries.ListAllApiTokens(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.AdminTokensPage(tokens, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// AdminTokensRevoke handles POST /admin/tokens/{id}/revoke. An admin
// can revoke any token regardless of owner.
func (h *TokensHandler) AdminTokensRevoke(
	w http.ResponseWriter,
	r *http.Request,
) {
	id := chi.URLParam(r, "id")
	if _, err := h.repo.Queries.GetApiTokenByID(r.Context(), id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if err := h.repo.Queries.RevokeApiToken(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirect := "/admin/tokens"
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirect)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}
