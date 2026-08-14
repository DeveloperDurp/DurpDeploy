package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

type mfaCompletion struct {
	Binding mfa.ChallengeBinding
	Factor  finalLoginFactor
	Mutate  func(context.Context, *db.Queries) error
}

func (h *AuthHandler) completeMFAForm(
	w http.ResponseWriter,
	r *http.Request,
	completion mfaCompletion,
) {
	if err := h.completeMFAChallenge(w, r, completion); err != nil {
		if isLoginMFAFactorFailure(err) {
			h.recordMFAFailure(w, r, completion.Binding)
			return
		}
		h.writeMFAFormError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) completeMFAJSON(
	w http.ResponseWriter,
	r *http.Request,
	completion mfaCompletion,
) {
	if err := h.completeMFAChallenge(w, r, completion); err != nil {
		h.writeMFAJSONError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).
		Encode(map[string]string{"redirect": "/"}); err != nil {
		h.writeMFAJSONError(w, r, err)
	}
}

func (h *AuthHandler) completeMFAChallenge(
	w http.ResponseWriter,
	r *http.Request,
	completion mfaCompletion,
) error {
	issue := h.finalBrowserSessionIssue(r, finalSessionIssue{
		UserID: completion.Binding.UserID,
		Factor: completion.Factor,
	})
	var session db.Session
	if err := h.challengeService().ConsumeWith(
		r.Context(),
		completion.Binding,
		func(
			ctx context.Context,
			queries *db.Queries,
			_ db.MfaChallenge,
		) error {
			if err := completion.Mutate(ctx, queries); err != nil {
				return err
			}
			created, err := auth.IssueBrowserSessionWith(ctx, queries, issue)
			if err != nil {
				return err
			}
			session = created
			return nil
		},
	); err != nil {
		return err
	}
	issue.Audit(session)
	h.setFinalBrowserSessionCookie(w, session)
	h.clearPendingMFACookies(w)
	return nil
}
