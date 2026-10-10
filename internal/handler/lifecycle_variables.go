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
