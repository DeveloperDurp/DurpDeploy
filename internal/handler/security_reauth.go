package handler

import (
	"context"
	"net/http"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/views/pages"
)

const reauthenticationError = "Unable to reauthenticate"

func (h *AuthHandler) SecurityReauthGet(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	_, session, ok := h.securityIdentity(r)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if err := pages.SecurityReauthPage(
		r.URL.Path,
		session.CsrfToken,
		"",
		h.oidcProvider != nil && h.oidcTransactions != nil,
	).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"render reauthentication page",
			http.StatusInternalServerError,
		)
	}
}

func (h *AuthHandler) SecurityReauthPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	user, session, ok := h.securityIdentity(r)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderReauthenticationError(w, r, session.CsrfToken)
		return
	}
	storedUser, err := h.repo.Queries.GetUserByID(r.Context(), user.ID)
	passwordHash, hasPassword := passwordHashForUser(storedUser, err)
	passwordMatches, verifyErr := h.verifyPassword(
		r.Context(),
		passwordHash,
		r.PostFormValue("password"),
	)
	if verifyErr != nil {
		return
	}
	if !passwordMatches || !hasPassword {
		h.renderReauthenticationError(w, r, session.CsrfToken)
		return
	}
	factors, err := h.repo.Queries.CountMFAFactors(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load MFA factors", http.StatusInternalServerError)
		return
	}
	if factors == 0 {
		h.finishSecurityReauthentication(w, r, session)
		return
	}
	audit.Suppress(r)
	pending, err := h.challengeService().Issue(r.Context(), mfa.ChallengeIssue{
		UserID: user.ID, SessionID: session.ID, Purpose: mfa.ChallengePurposeTOTPVerify,
	})
	if err != nil {
		http.Error(
			w,
			"create reauthentication challenge",
			http.StatusInternalServerError,
		)
		return
	}
	if err := pages.SecurityReauthMFAPage(
		r.URL.Path,
		session.CsrfToken,
		pending.Token,
		pending.CSRF,
		"",
	).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"render factor reauthentication",
			http.StatusInternalServerError,
		)
	}
}

func (h *AuthHandler) SecurityReauthTOTPPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.currentSecurityChallenge(r, mfa.ChallengePurposeTOTPVerify)
	if err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	code := r.FormValue("code")
	h.completeSecurityReauthentication(
		w,
		r,
		state.Binding,
		func(ctx context.Context, queries *db.Queries) error {
			return factors.VerifyTOTPWith(
				ctx,
				queries,
				state.Binding.UserID,
				code,
			)
		},
	)
}

func (h *AuthHandler) SecurityReauthRecoveryPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.currentSecurityChallenge(r, mfa.ChallengePurposeTOTPVerify)
	if err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	code := r.FormValue("code")
	usedAt := time.Now().Unix()
	h.completeSecurityReauthentication(
		w,
		r,
		state.Binding,
		func(ctx context.Context, queries *db.Queries) error {
			return factors.ConsumeRecoveryWith(
				ctx,
				queries,
				state.Binding.UserID,
				code,
				usedAt,
			)
		},
	)
}

func (h *AuthHandler) completeSecurityReauthentication(
	w http.ResponseWriter,
	r *http.Request,
	binding mfa.ChallengeBinding,
	verify func(context.Context, *db.Queries) error,
) {
	_, session, ok := h.securityIdentity(r)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if err := h.challengeService().ConsumeWith(r.Context(), binding, func(
		ctx context.Context,
		queries *db.Queries,
		_ db.MfaChallenge,
	) error {
		return verify(ctx, queries)
	}); err != nil {
		h.recordSecurityFailure(w, r, binding)
		return
	}
	h.finishSecurityReauthentication(w, r, session)
}

func (h *AuthHandler) recordSecurityFailure(
	w http.ResponseWriter,
	r *http.Request,
	binding mfa.ChallengeBinding,
) {
	h.writeSecurityChallengeError(
		w,
		h.challengeService().RecordFailure(r.Context(), binding),
	)
}

func (h *AuthHandler) writeSecurityChallengeError(
	w http.ResponseWriter,
	err error,
) {
	http.Error(w, reauthenticationError, securityErrorStatus(err))
}

func (h *AuthHandler) renderReauthenticationError(
	w http.ResponseWriter,
	r *http.Request,
	csrf string,
) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := pages.SecurityReauthPage(
		r.URL.Path,
		csrf,
		reauthenticationError,
		h.oidcProvider != nil && h.oidcTransactions != nil,
	).
		Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"render reauthentication error",
			http.StatusInternalServerError,
		)
	}
}
