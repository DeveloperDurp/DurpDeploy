package handler

import (
	"context"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func (h *AuthHandler) LoginMFATOTPPost(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	state, err := h.pendingMFAChallenge(r, mfa.ChallengePurposeLoginMFA)
	if err != nil {
		h.writeMFAFormError(w, r, err)
		return
	}
	availability, err := h.loginMFAAvailability(
		r.Context(),
		state.Binding.UserID,
	)
	if err != nil || !availability.TOTP {
		h.writeMFAFormErrorWithAvailability(
			w,
			r,
			mfa.ErrMFAFactorOperation,
			availability,
		)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeMFAFormError(w, r, err)
		return
	}
	code := r.FormValue("code")
	h.completeMFAForm(w, r, mfaCompletion{
		Binding: state.Binding,
		Factor:  finalLoginTOTP,
		Mutate: func(ctx context.Context, queries *db.Queries) error {
			return factors.VerifyTOTPWith(
				ctx,
				queries,
				state.Binding.UserID,
				code,
			)
		},
	})
}

func (h *AuthHandler) LoginMFARecoveryPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.pendingMFAChallenge(r, mfa.ChallengePurposeLoginMFA)
	if err != nil {
		h.writeMFAFormError(w, r, err)
		return
	}
	availability, err := h.loginMFAAvailability(
		r.Context(),
		state.Binding.UserID,
	)
	if err != nil || !availability.Recovery {
		h.writeMFAFormErrorWithAvailability(
			w,
			r,
			mfa.ErrMFAFactorOperation,
			availability,
		)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeMFAFormError(w, r, err)
		return
	}
	code := r.FormValue("code")
	usedAt := time.Now().Unix()
	h.completeMFAForm(w, r, mfaCompletion{
		Binding: state.Binding,
		Factor:  finalLoginRecovery,
		Mutate: func(ctx context.Context, queries *db.Queries) error {
			return factors.ConsumeRecoveryWith(
				ctx,
				queries,
				state.Binding.UserID,
				code,
				usedAt,
			)
		},
	})
}
