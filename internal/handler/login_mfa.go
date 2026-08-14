package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"durpdeploy/internal/mfa"
	"durpdeploy/views/pages"
)

const mfaFactorError = "Unable to verify authentication factor"

func (h *AuthHandler) LoginMFAGet(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	pending, err := r.Cookie("mfa_pending")
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	csrf, err := r.Cookie("mfa_csrf")
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	state, err := h.challengeService().ResolveLogin(
		r.Context(), pending.Value, csrf.Value,
	)
	if err != nil {
		h.clearPendingMFACookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	availability, err := h.loginMFAAvailability(
		r.Context(),
		state.Binding.UserID,
	)
	if err != nil {
		availability = pages.LoginMFAAvailability{}
	}
	if err := pages.LoginMFAPage(r.URL.Path, pendingCSRF(r), availability, "").
		Render(r.Context(), w); err != nil {
		http.Error(w, "render MFA login page", http.StatusInternalServerError)
	}
}

func (h *AuthHandler) LoginMFACancelPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.pendingLoginMFAChallenge(r)
	if err != nil {
		h.writeMFAFormError(w, r, err)
		return
	}
	if err := h.challengeService().
		Cancel(r.Context(), state.Binding); err != nil {
		h.writeMFAFormError(w, r, err)
		return
	}
	h.clearPendingMFACookies(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *AuthHandler) pendingMFAChallenge(
	r *http.Request,
	purpose mfa.ChallengePurpose,
) (mfa.ResolvedChallenge, error) {
	token, csrf, err := pendingMFACredentials(r)
	if err != nil {
		return mfa.ResolvedChallenge{}, err
	}
	return h.challengeService().Resolve(r.Context(), token, csrf, purpose)
}

func (h *AuthHandler) pendingLoginMFAChallenge(
	r *http.Request,
) (mfa.ResolvedChallenge, error) {
	token, csrf, err := pendingMFACredentials(r)
	if err != nil {
		return mfa.ResolvedChallenge{}, err
	}
	return h.challengeService().ResolveLogin(r.Context(), token, csrf)
}

func pendingMFACredentials(r *http.Request) (string, string, error) {
	pending, err := r.Cookie("mfa_pending")
	if err != nil {
		return "", "", mfa.ErrChallengeCSRF
	}
	csrfCookie, err := r.Cookie("mfa_csrf")
	if err != nil {
		return "", "", mfa.ErrChallengeCSRF
	}
	csrf := r.Header.Get("X-CSRF-Token")
	if csrf == "" {
		if err := r.ParseForm(); err != nil {
			return "", "", mfa.ErrChallengeCSRF
		}
		csrf = r.PostFormValue("csrf_token")
	}
	if csrf == "" || subtle.ConstantTimeCompare(
		[]byte(csrfCookie.Value),
		[]byte(csrf),
	) != 1 {
		return "", "", mfa.ErrChallengeCSRF
	}
	return pending.Value, csrf, nil
}

func (h *AuthHandler) recordMFAFailure(
	w http.ResponseWriter,
	r *http.Request,
	binding mfa.ChallengeBinding,
) {
	h.writeMFAFormError(
		w,
		r,
		h.challengeService().RecordFailure(r.Context(), binding),
	)
}

func (h *AuthHandler) recordMFAFailureJSON(
	w http.ResponseWriter,
	r *http.Request,
	binding mfa.ChallengeBinding,
) {
	h.writeMFAJSONError(
		w,
		r,
		h.challengeService().RecordFailure(r.Context(), binding),
	)
}

func isLoginMFAFactorFailure(err error) bool {
	return errors.Is(err, mfa.ErrInvalidTOTP) ||
		errors.Is(err, mfa.ErrTOTPReplay) ||
		errors.Is(err, mfa.ErrInvalidRecoveryCode)
}

func (h *AuthHandler) writeMFAFormError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	availability := pages.LoginMFAAvailability{}
	state, stateErr := h.pendingLoginMFAChallenge(r)
	if stateErr == nil {
		resolved, availabilityErr := h.loginMFAAvailability(
			r.Context(),
			state.Binding.UserID,
		)
		if availabilityErr == nil {
			availability = resolved
		}
	}
	h.writeMFAFormErrorWithAvailability(w, r, err, availability)
}

func (h *AuthHandler) writeMFAFormErrorWithAvailability(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	availability pages.LoginMFAAvailability,
) {
	if errors.Is(err, mfa.ErrChallengeInvalid) {
		h.clearPendingMFACookies(w)
	}
	w.WriteHeader(mfaErrorStatus(err))
	_ = pages.LoginMFAPage(r.URL.Path, pendingCSRF(r), availability, mfaFactorError).
		Render(r.Context(), w)
}

func (h *AuthHandler) writeMFAJSONError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	if errors.Is(err, mfa.ErrChallengeInvalid) {
		h.clearPendingMFACookies(w)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(mfaErrorStatus(err))
	_ = json.NewEncoder(w).Encode(map[string]string{"error": mfaFactorError})
}

func mfaErrorStatus(err error) int {
	switch {
	case errors.Is(err, mfa.ErrChallengeCSRF):
		return http.StatusForbidden
	case errors.Is(err, mfa.ErrMFACooldown):
		return http.StatusTooManyRequests
	case err == nil:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusUnprocessableEntity
	}
}

func (h *AuthHandler) challengeService() *mfa.ChallengeService {
	if h.mfaService != nil {
		return h.mfaService.Challenges(h.repo)
	}
	return mfa.NewChallengeService(
		mfa.ChallengeServiceConfig{Repository: h.repo},
	)
}

func (h *AuthHandler) mfaFactors() (*mfa.FactorStore, error) {
	if h.mfaService == nil {
		return nil, mfa.ErrMFAFactorOperation
	}
	return h.mfaService.Factors(h.repo), nil
}
