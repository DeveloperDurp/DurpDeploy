package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/views/pages"
)

type pendingTOTPEnrollment struct {
	EncryptedSeed string `json:"encrypted_seed"`
}

func (h *AuthHandler) SecurityTOTPBeginPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, session, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	configured, err := h.hasTOTP(r.Context(), user.ID)
	if err != nil {
		http.Error(w, securityActionError, http.StatusInternalServerError)
		return
	}
	if configured {
		http.Error(w, securityActionError, http.StatusUnprocessableEntity)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		http.Error(w, securityActionError, http.StatusUnprocessableEntity)
		return
	}
	enrollment, encryptedSeed, err := factors.BeginTOTPEnrollment(user.Email)
	if err != nil {
		http.Error(w, securityActionError, http.StatusInternalServerError)
		return
	}
	ceremony, err := json.Marshal(pendingTOTPEnrollment{
		EncryptedSeed: encryptedSeed,
	})
	if err != nil {
		http.Error(w, securityActionError, http.StatusInternalServerError)
		return
	}
	pending, err := h.challengeService().Issue(r.Context(), mfa.ChallengeIssue{
		UserID: user.ID, SessionID: session.ID,
		Purpose: mfa.ChallengePurposeTOTPEnroll, CeremonyJSON: string(ceremony),
	})
	if err != nil {
		http.Error(w, securityActionError, http.StatusInternalServerError)
		return
	}
	if err := pages.TOTPEnrollmentPage(
		r.URL.Path,
		session.CsrfToken,
		pending.Token,
		pending.CSRF,
		enrollment.Seed,
		base64.StdEncoding.EncodeToString(enrollment.PNG),
		"",
	).Render(r.Context(), w); err != nil {
		http.Error(w, "render TOTP enrollment", http.StatusInternalServerError)
	}
}

func (h *AuthHandler) SecurityTOTPConfirmPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, session, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	state, err := h.currentSecurityChallenge(r, mfa.ChallengePurposeTOTPEnroll)
	if err != nil {
		h.writeTOTPEnrollmentError(w, err)
		return
	}
	var enrollment pendingTOTPEnrollment
	if err := json.Unmarshal(
		[]byte(state.CeremonyJSON),
		&enrollment,
	); err != nil {
		h.writeTOTPEnrollmentError(w, mfa.ErrChallengeInvalid)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeTOTPEnrollmentError(w, err)
		return
	}
	seed, step, err := factors.ConfirmTOTPEnrollment(
		enrollment.EncryptedSeed,
		r.FormValue("code"),
		-1,
	)
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	var codes []string
	err = h.challengeService().ConsumeWith(r.Context(), state.Binding, func(
		ctx context.Context,
		queries *db.Queries,
		_ db.MfaChallenge,
	) error {
		var activationErr error
		codes, activationErr = factors.ActivateTOTPWith(
			ctx,
			queries,
			user.ID,
			seed,
			step,
		)
		return activationErr
	})
	if err != nil {
		h.writeTOTPEnrollmentError(w, err)
		return
	}
	h.clearBrowserSessionCookie(w)
	if len(codes) == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := pages.RecoveryCodesPage(
		r.URL.Path,
		codes,
		session.CsrfToken,
		true,
	).Render(r.Context(), w); err != nil {
		http.Error(w, "render recovery codes", http.StatusInternalServerError)
	}
}

func (h *AuthHandler) writeTOTPEnrollmentError(
	w http.ResponseWriter,
	err error,
) {
	http.Error(w, securityActionError, securityErrorStatus(err))
}
