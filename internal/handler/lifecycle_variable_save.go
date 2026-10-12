package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func (h *LifecycleVariableHandler) Save(
	w http.ResponseWriter,
	r *http.Request,
) {
	data, ok := h.workspace(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	id, err := lifecycleVariableFormID(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var env sql.NullInt64
	scope := r.FormValue("environment_id")
	if scope == "" && id != 0 {
		for _, variable := range data.Variables {
			if variable.ID == id && variable.EnvironmentID.Valid {
				http.Error(w, "Choose an explicit environment scope", 400)
				return
			}
		}
	}
	if scope != "" && scope != "all" {
		envID, err := strconv.ParseInt(scope, 10, 64)
		if err != nil || envID <= 0 {
			http.Error(w, "Invalid environment ID", 400)
			return
		}
		env = sql.NullInt64{Int64: envID, Valid: true}
	}
	var secret int64
	if r.FormValue("secret") == "on" {
		secret = 1
	}
	_, err = h.repo.SaveLifecycleVariable(
		r.Context(),
		repository.LifecycleVariableInput{
			ID: id, CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
				LifecycleID: data.Lifecycle.ID, Name: r.FormValue("name"),
				Value: sql.NullString{
					String: r.FormValue("value"),
					Valid:  r.FormValue("value") != "",
				},
				EnvironmentID: env, Secret: secret,
			},
		},
	)
	if err != nil {
		if errors.Is(err, repository.ErrVariableName) ||
			errors.Is(err, repository.ErrLifecycleVariableScope) ||
			ArtifactErrorStatus(err) == 422 ||
			IsUniqueViolation(err) {
			data.Error = "A variable with this name and scope already exists"
			if !IsUniqueViolation(err) {
				data.Error = err.Error()
			}
			data.Editing = db.LifecycleVariable{
				ID:            id,
				Name:          r.FormValue("name"),
				EnvironmentID: env,
				Secret:        secret,
			}
			if secret == 0 {
				data.Editing.Value = sql.NullString{
					String: r.FormValue("value"), Valid: true,
				}
			}
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Retarget", "#shared-variables")
				w.Header().Set("HX-Reswap", "outerHTML")
			}
			w.WriteHeader(422)
			h.render(w, r, data)
		} else {
			lifecycleVariableWebError(w, err)
		}
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.List(w, r)
	} else {
		http.Redirect(w, r, fmt.Sprintf("/lifecycles/%d#shared-variables", data.Lifecycle.ID), http.StatusSeeOther)
	}
}
