package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/verification"
	"durpdeploy/views/pages"
)

func environmentVerificationForm(
	r *http.Request,
) (verification.Settings, error) {
	timeout := verification.DefaultTimeout
	if raw := r.FormValue("verification_timeout_seconds"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 || parsed > 300 {
			return verification.Settings{}, verification.ErrInvalid
		}
		timeout = parsed
	}
	return verification.Parse(r.FormValue("verification_type"),
		r.FormValue("verification_target"), timeout)
}

func environmentVerificationAllowed(
	w http.ResponseWriter,
	r *http.Request,
) bool {
	for _, name := range []string{"verification_type", "verification_target", "verification_timeout_seconds"} {
		if r.Form.Has(name) &&
			auth.RoleFromContext(r.Context()) != "admin" {
			auth.RenderUnauthorized(w, r)
			return false
		}
	}
	return true
}

func environmentVerificationError(
	w http.ResponseWriter, r *http.Request, id int64, isNew bool,
) {
	env := &db.Environment{
		ID:   id,
		Name: r.FormValue("name"),
		Description: sql.NullString{
			String: r.FormValue("description"),
			Valid:  r.FormValue("description") != "",
		},
		Tags: sql.NullString{
			String: r.FormValue("tags"),
			Valid:  r.FormValue("tags") != "",
		},
		VerificationType:           r.FormValue("verification_type"),
		VerificationTarget:         r.FormValue("verification_target"),
		VerificationTimeoutSeconds: verification.DefaultTimeout,
	}
	WriteFormError(w, r,
		pages.EnvironmentFormFragment(env, isNew,
			verification.ErrInvalid.Error()),
		pages.EnvironmentForm(env, isNew,
			verification.ErrInvalid.Error(), r.URL.Path))
}
