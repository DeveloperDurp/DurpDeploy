package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"durpdeploy/internal/auth"
)

// PasswordChangeRequest contains only credentials for the authenticated owner.
type PasswordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	Confirmation    string `json:"password_confirmation"`
}

var errPasswordInput = errors.New(
	"enter the current password and matching new passwords of at least 8 characters",
)

var errCurrentPassword = errors.New(
	"the current password is incorrect or the account has no local password",
)

var errPasswordBrowserInput = errors.New(
	"the new password is too long or contains line breaks",
)

func (h *AuthHandler) changeOwnPassword(
	ctx context.Context, userID int64, input PasswordChangeRequest,
) error {
	if input.CurrentPassword == "" || len(input.NewPassword) < 8 ||
		input.NewPassword != input.Confirmation {
		return errPasswordInput
	}
	user, err := h.repo.Queries.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	// Reserve all three password fields for later browser rotation, plus
	// email and CSRF for login/reauthentication. Browsers also escape tilde.
	form := url.Values{
		"email": {user.Email}, "current_password": {input.NewPassword},
		"new_password":          {input.NewPassword},
		"password_confirmation": {input.NewPassword},
		"csrf_token":            {strings.Repeat("0", 32)},
	}
	encoded := strings.ReplaceAll(form.Encode(), "~", "%7E")
	if strings.ContainsAny(input.NewPassword, "\r\n") ||
		int64(len(encoded)) > h.repo.ResourceLimits().HTTP.PasswordFormBytes {
		return errPasswordBrowserInput
	}
	var updateErr error
	matched, err := h.withPasswordVerification(ctx, func() bool {
		if user.PasswordHash == "" ||
			!auth.VerifyPassword(user.PasswordHash, input.CurrentPassword) {
			return false
		}
		updateErr = auth.UpdatePassword(ctx, h.repo, auth.PasswordChange{
			UserID: userID, Password: input.NewPassword,
			ExpectedHash: user.PasswordHash,
		})
		return true
	})
	if err != nil {
		return err
	}
	if !matched || errors.Is(updateErr, auth.ErrPasswordChanged) {
		return errCurrentPassword
	}
	return updateErr
}

func (h *AuthHandler) SecurityPasswordPost(
	w http.ResponseWriter, r *http.Request,
) {
	user, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	input := PasswordChangeRequest{
		CurrentPassword: r.PostFormValue("current_password"),
		NewPassword:     r.PostFormValue("new_password"),
		Confirmation:    r.PostFormValue("password_confirmation"),
	}
	if err := h.changeOwnPassword(r.Context(), user.ID, input); err != nil {
		if errors.Is(err, errPasswordInput) ||
			errors.Is(err, errCurrentPassword) ||
			errors.Is(err, errPasswordBrowserInput) {
			h.renderSecurityPage(w, r, err.Error())
		} else {
			http.Error(w, securityActionError, http.StatusInternalServerError)
		}
		return
	}
	h.clearBrowserSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *AuthHandler) SecurityPasswordAPI(
	w http.ResponseWriter, r *http.Request,
) {
	setNoStore(w)
	user := auth.UserFromContext(r.Context())
	token := auth.TokenFromContext(r.Context())
	if user == nil || token == nil || token.Scope != "global" ||
		token.Role != "" {
		auth.RenderJSONError(w, http.StatusForbidden,
			"an unrestricted global token is required")
		return
	}
	var input PasswordChangeRequest
	if !ReadJSON(w, r, &input) {
		return
	}
	if err := h.changeOwnPassword(r.Context(), user.ID, input); err != nil {
		status, message := http.StatusInternalServerError, securityActionError
		if errors.Is(err, errPasswordInput) ||
			errors.Is(err, errCurrentPassword) ||
			errors.Is(err, errPasswordBrowserInput) {
			status, message = http.StatusUnprocessableEntity, err.Error()
		}
		auth.RenderJSONError(w, status, message)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
