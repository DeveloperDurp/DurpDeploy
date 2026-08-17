package handler

import (
	"log/slog"
	"net/http"

	"durpdeploy/internal/oidc"
	"durpdeploy/views/pages"
)

const oidcFailurePath = "/login/oidc/failure"

func (h *AuthHandler) LoginOIDCCallbackGet(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	if h.oidcProvider == nil || h.oidcTransactions == nil {
		slog.Info("oidc initial login callback failed", "stage", "transaction")
		h.redirectOIDCLoginFailure(w, r)
		return
	}

	query := r.URL.Query()
	transaction, err := h.oidcTransactions.Consume(
		r.Context(),
		oidc.CallbackRequest{
			Request:          r,
			Response:         w,
			State:            query.Get("state"),
			HasProviderError: query.Has("error"),
		},
	)
	if err != nil {
		slog.Info("oidc initial login callback failed", "stage", "transaction")
		h.redirectOIDCLoginFailure(w, r)
		return
	}

	switch transaction.Mode {
	case oidc.TransactionModeLogin:
		claims, err := h.oidcProvider.Exchange(
			r.Context(),
			query.Get("code"),
			transaction,
		)
		if err != nil {
			slog.Info("oidc initial login callback failed", "stage", "exchange")
			h.redirectOIDCLoginFailure(w, r)
			return
		}
		identity, err := h.oidcProvider.ParseClaims(claims)
		if err != nil {
			slog.Info("oidc initial login callback failed", "stage", "claims")
			h.redirectOIDCLoginFailure(w, r)
			return
		}
		user, err := oidc.ResolveIdentity(r.Context(), h.repo, identity)
		if err != nil {
			slog.Info("oidc initial login callback failed", "stage", "identity")
			h.redirectOIDCLoginFailure(w, r)
			return
		}
		if err := h.issueFinalBrowserSession(w, r, finalSessionIssue{
			UserID: user.ID,
			Factor: finalLoginOIDC,
		}); err != nil {
			slog.Info("oidc initial login callback failed", "stage", "session")
			h.redirectOIDCLoginFailure(w, r)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case oidc.TransactionModeReauth:
		h.completeOIDCReauthentication(w, r, query.Get("code"), transaction)
	default:
		slog.Info(
			"oidc initial login callback failed",
			"stage",
			"unsupported_mode",
		)
		h.redirectOIDCLoginFailure(w, r)
	}
}

func (h *AuthHandler) LoginOIDCFailureGet(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = pages.LoginPage(
		r.URL.Path,
		"Single sign-on could not be completed",
		h.oidcDisplayName,
	).Render(r.Context(), w)
}

func (h *AuthHandler) redirectOIDCLoginFailure(
	w http.ResponseWriter,
	r *http.Request,
) {
	http.Redirect(w, r, oidcFailurePath, http.StatusSeeOther)
}
