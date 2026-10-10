package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type LifecycleVariableHandler struct{ repo *repository.Repository }

func NewLifecycleVariableHandler(
	repo *repository.Repository,
) *LifecycleVariableHandler {
	return &LifecycleVariableHandler{repo: repo}
}

func loadLifecycleVariables(
	ctx context.Context,
	repo *repository.Repository,
	lifecycle db.Lifecycle,
) (pages.LifecycleVariables, error) {
	data := pages.LifecycleVariables{Lifecycle: lifecycle}
	var err error
	data.Variables, err = repo.ListLifecycleVariables(ctx, lifecycle.ID)
	if err != nil {
		return data, err
	}
	data.Environments, err = repo.Queries.ListEnvironments(ctx)
	if err != nil {
		return data, err
	}
	stages, err := repo.Queries.ListLifecycleStageEnvironmentIDs(
		ctx,
		lifecycle.ID,
	)
	if err != nil {
		return data, err
	}
	for _, env := range data.Environments {
		for _, id := range stages {
			if id == env.ID {
				data.Options = append(data.Options, env)
				break
			}
		}
	}
	data.Projects, err = repo.Queries.ListProjectsByLifecycle(
		ctx,
		sql.NullInt64{Int64: lifecycle.ID, Valid: true},
	)
	return data, err
}

func (h *LifecycleVariableHandler) workspace(
	w http.ResponseWriter,
	r *http.Request,
) (pages.LifecycleVariables, bool) {
	id, err := parseLifecycleID(r)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid lifecycle ID", 400)
		return pages.LifecycleVariables{}, false
	}
	lifecycle, err := h.repo.Queries.GetLifecycle(r.Context(), id)
	if err != nil {
		lifecycleVariableWebError(w, err)
		return pages.LifecycleVariables{}, false
	}
	data, err := loadLifecycleVariables(r.Context(), h.repo, lifecycle)
	if err != nil {
		lifecycleVariableWebError(w, err)
		return data, false
	}
	return data, true
}

func lifecycleVariableWebError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Lifecycle or variable not found", 404)
		return
	}
	http.Error(w, "Unable to access lifecycle variables", 500)
}

func lifecycleVariableFormID(r *http.Request) (int64, error) {
	value := chi.URLParam(r, "varId")
	if value == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid variable ID")
	}
	return id, nil
}

func (h *LifecycleVariableHandler) render(
	w http.ResponseWriter,
	r *http.Request,
	data pages.LifecycleVariables,
) {
	if r.Header.Get("HX-Request") == "true" {
		if err := pages.SharedLifecycleVariables(data).Render(r.Context(), w); err != nil {
			lifecycleVariableWebError(w, err)
		}
	} else {
		if err := pages.LifecycleVariablesPage(data, r.URL.Path).Render(r.Context(), w); err != nil {
			lifecycleVariableWebError(w, err)
		}
	}
}

func (h *LifecycleVariableHandler) List(
	w http.ResponseWriter,
	r *http.Request,
) {
	data, ok := h.workspace(w, r)
	if ok {
		h.render(w, r, data)
	}
}

func (h *LifecycleVariableHandler) Edit(
	w http.ResponseWriter,
	r *http.Request,
) {
	data, ok := h.workspace(w, r)
	if !ok {
		return
	}
	id, err := lifecycleVariableFormID(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	data.Editing, err = h.repo.GetLifecycleVariable(
		r.Context(),
		db.GetLifecycleVariableParams{ID: id, LifecycleID: data.Lifecycle.ID},
	)
	if err != nil {
		lifecycleVariableWebError(w, err)
		return
	}
	h.render(w, r, data)
}

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
	if scope := r.FormValue("environment_id"); scope != "" {
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

func (h *LifecycleVariableHandler) Delete(
	w http.ResponseWriter,
	r *http.Request,
) {
	data, ok := h.workspace(w, r)
	if !ok {
		return
	}
	id, err := lifecycleVariableFormID(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	count, err := h.repo.Queries.DeleteLifecycleVariable(
		r.Context(),
		db.DeleteLifecycleVariableParams{
			ID:          id,
			LifecycleID: data.Lifecycle.ID,
		},
	)
	if err != nil {
		lifecycleVariableWebError(w, err)
		return
	}
	if count == 0 {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.List(w, r)
	} else {
		http.Redirect(w, r, fmt.Sprintf("/lifecycles/%d#shared-variables", data.Lifecycle.ID), http.StatusSeeOther)
	}
}
