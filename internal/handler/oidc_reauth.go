package handler

import (
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/oidc"
)

const oidcReauthContinuation = "/settings/security"

// SecurityReauthOIDCGet begins a same-subject OIDC fresh-authentication flow.
func (h *AuthHandler) SecurityReauthOIDCGet(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	user, session, ok := h.securityIdentity(r)
	if !ok || h.oidcProvider == nil || h.oidcTransactions == nil {
		h.renderOIDCReauthFailure(w)
		return
	}
	identity, err := h.repo.Queries.GetOIDCIdentityByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil || identity.Issuer != h.oidcProvider.Issuer() {
		h.renderOIDCReauthFailure(w)
		return
	}
	transaction, cookie, err := h.oidcTransactions.Start(
		r.Context(),
		oidc.TransactionRequest{
			Mode: oidc.TransactionModeReauth,
			Reauth: oidc.ReauthBinding{
				SessionID:       session.ID,
				LocalUserID:     user.ID,
				ExpectedIssuer:  identity.Issuer,
				ExpectedSubject: identity.Subject,
				Continuation:    oidcReauthContinuation,
			},
		},
	)
	if err != nil {
		h.renderOIDCReauthFailure(w)
		return
	}
	location, err := h.oidcProvider.AuthorizationURL(r.Context(), transaction)
	if err != nil {
		h.renderOIDCReauthFailure(w)
		return
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (h *AuthHandler) completeOIDCReauthentication(
	w http.ResponseWriter,
	r *http.Request,
	code string,
	transaction oidc.Transaction,
) {
	user, session, ok := h.securityIdentity(r)
	if !ok || session.ID != transaction.Reauth.SessionID ||
		user.ID != transaction.Reauth.LocalUserID ||
		transaction.Reauth.Continuation != oidcReauthContinuation {
		h.redirectOIDCLoginFailure(w, r)
		return
	}
	claims, err := h.oidcProvider.Exchange(r.Context(), code, transaction)
	if err != nil {
		h.redirectOIDCLoginFailure(w, r)
		return
	}
	identity, err := h.oidcProvider.ParseReauthenticationClaims(
		claims,
		transaction,
	)
	if err != nil || identity.Issuer != transaction.Reauth.ExpectedIssuer ||
		identity.Subject != transaction.Reauth.ExpectedSubject {
		h.redirectOIDCLoginFailure(w, r)
		return
	}
	stored, err := h.repo.Queries.GetOIDCIdentity(
		r.Context(),
		db.GetOIDCIdentityParams{
			Issuer:  transaction.Reauth.ExpectedIssuer,
			Subject: transaction.Reauth.ExpectedSubject,
		},
	)
	if err != nil || stored.UserID != user.ID {
		h.redirectOIDCLoginFailure(w, r)
		return
	}
	h.finishSecurityReauthentication(w, r, session)
}

func (h *AuthHandler) renderOIDCReauthFailure(w http.ResponseWriter) {
	http.Error(w, reauthenticationError, http.StatusUnprocessableEntity)
}
