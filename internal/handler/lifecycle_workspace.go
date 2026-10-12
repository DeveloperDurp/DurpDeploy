package handler

import (
	"context"
	"database/sql"
	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
	"fmt"
	"net/http"
)

func (h *LifecycleHandler) GetLifecycle(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseLifecycleID(r)
	if err != nil {
		http.Error(w, "Invalid lifecycle ID", http.StatusBadRequest)
		return
	}

	lc, err := h.repo.Queries.GetLifecycle(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Lifecycle not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	stageViews, availableEnvs, err := h.lifecycleWorkspace(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	workspace := pages.LifecycleWorkspace{
		Lifecycle:             lc,
		Stages:                stageViews,
		AvailableEnvironments: availableEnvs,
	}
	if pages.CanManageLifecycleVariables(r.Context()) &&
		r.Header.Get("HX-Target") != "project-edit-content" &&
		r.Header.Get("X-Form-Dialog") != "true" {
		workspace.Shared, err = loadLifecycleVariables(r.Context(), h.repo, lc)
		if err != nil {
			lifecycleVariableWebError(w, err)
			return
		}
	}
	if err := pages.LifecycleDetailPage(workspace, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *LifecycleHandler) lifecycleWorkspace(
	ctx context.Context,
	lifecycleID int64,
) ([]pages.LifecycleStageView, []db.Environment, error) {
	stages, err := h.repo.Queries.ListLifecycleStages(ctx, lifecycleID)
	if err != nil {
		return nil, nil, fmt.Errorf("list lifecycle stages: %w", err)
	}
	environments, err := h.repo.Queries.ListEnvironments(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list environments: %w", err)
	}

	envsByID := make(map[int64]db.Environment, len(environments))
	for _, env := range environments {
		envsByID[env.ID] = env
	}
	used := make(map[int64]bool, len(stages))
	stageViews := make([]pages.LifecycleStageView, len(stages))
	for i, stage := range stages {
		used[stage.EnvironmentID] = true
		stageViews[i] = pages.LifecycleStageView{
			Stage:       stage,
			Environment: envsByID[stage.EnvironmentID],
		}
	}

	availableEnvs := make([]db.Environment, 0, len(environments)-len(used))
	for _, env := range environments {
		if !used[env.ID] {
			availableEnvs = append(availableEnvs, env)
		}
	}
	return stageViews, availableEnvs, nil
}

func (h *LifecycleHandler) writeLifecycleDetailError(
	w http.ResponseWriter,
	r *http.Request,
	lifecycle db.Lifecycle,
	message string,
) error {
	stages, availableEnvs, err := h.lifecycleWorkspace(
		r.Context(), lifecycle.ID,
	)
	if err != nil {
		return err
	}
	workspace := pages.LifecycleWorkspace{
		Lifecycle:             lifecycle,
		Stages:                stages,
		AvailableEnvironments: availableEnvs,
		Error:                 message,
	}
	if pages.CanManageLifecycleVariables(r.Context()) &&
		r.Header.Get("HX-Target") != "project-edit-content" &&
		r.Header.Get("X-Form-Dialog") != "true" {
		workspace.Shared, err = loadLifecycleVariables(
			r.Context(),
			h.repo,
			lifecycle,
		)
		if err != nil {
			return err
		}
	}
	WriteFormError(
		w,
		r,
		pages.LifecycleDetail(workspace),
		pages.LifecycleDetailPage(workspace, r.URL.Path),
	)
	return nil
}
