package handler

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/components"
	"durpdeploy/views/pages"
)

type StepHandler struct {
	repo *repository.Repository
}

func NewStepHandler(repo *repository.Repository) *StepHandler {
	return &StepHandler{repo: repo}
}

func (h *StepHandler) ListSteps(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components.StepList(steps, projectID).Render(r.Context(), w)
}

// StepsPage renders the dedicated full page for a project's steps. The
// project overview links here from its "Steps" button; the page hosts
// the same add/reorder/save-as-template UI that used to live inline on
// the project detail page.
func (h *StepHandler) StepsPage(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.StepsPage(project, steps, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepHandler) NewStepForm(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	step := db.Step{ProjectID: projectID}
	components.StepForm(step, projectID, true, "").Render(r.Context(), w)
}

func (h *StepHandler) CreateStep(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	script := r.FormValue("script_body")

	timeoutStr := strings.TrimSpace(r.FormValue("timeout_seconds"))
	var timeoutSeconds int64
	if timeoutStr != "" {
		t, err := strconv.ParseInt(timeoutStr, 10, 64)
		if err != nil || t < 0 {
			step := db.Step{
				ProjectID:      projectID,
				Name:           name,
				ScriptBody:     script,
				TimeoutSeconds: timeoutSeconds,
			}
			WriteFormError(
				w,
				r,
				components.StepForm(
					step,
					projectID,
					true,
					"Timeout must be a non-negative integer",
				),
				components.StepForm(
					step,
					projectID,
					true,
					"Timeout must be a non-negative integer",
				),
			)
			return
		}
		timeoutSeconds = t
	}

	maxRetriesStr := strings.TrimSpace(r.FormValue("max_retries"))
	var maxRetries int64
	if maxRetriesStr != "" {
		m, err := strconv.ParseInt(maxRetriesStr, 10, 64)
		if err != nil || m < 0 {
			step := db.Step{
				ProjectID:      projectID,
				Name:           name,
				ScriptBody:     script,
				TimeoutSeconds: timeoutSeconds,
				MaxRetries:     maxRetries,
			}
			WriteFormError(
				w,
				r,
				components.StepForm(
					step,
					projectID,
					true,
					"Max retries must be a non-negative integer",
				),
				components.StepForm(
					step,
					projectID,
					true,
					"Max retries must be a non-negative integer",
				),
			)
			return
		}
		maxRetries = m
	}

	if name == "" {
		step := db.Step{
			ProjectID:      projectID,
			Name:           name,
			ScriptBody:     script,
			TimeoutSeconds: timeoutSeconds,
			MaxRetries:     maxRetries,
		}
		WriteFormError(
			w,
			r,
			components.StepForm(step, projectID, true, "Name is required"),
			components.StepForm(step, projectID, true, "Name is required"),
		)
		return
	}

	var sortOrder int64
	if v := r.FormValue("sort_order"); v != "" {
		sortOrder, _ = strconv.ParseInt(v, 10, 64)
	} else {
		steps, _ := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
		for _, s := range steps {
			if s.SortOrder >= sortOrder {
				sortOrder = s.SortOrder + 1
			}
		}
	}

	params := db.CreateStepParams{
		ProjectID:      projectID,
		Name:           name,
		ScriptBody:     script,
		SortOrder:      sortOrder,
		TimeoutSeconds: timeoutSeconds,
		MaxRetries:     maxRetries,
	}

	_, err = h.repo.Queries.CreateStep(r.Context(), params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components.StepList(steps, projectID).Render(r.Context(), w)
}

func (h *StepHandler) EditStepForm(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	stepIDStr := chi.URLParam(r, "stepId")
	stepID, err := strconv.ParseInt(stepIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid step ID", http.StatusBadRequest)
		return
	}

	step, err := h.repo.Queries.GetStep(r.Context(), stepID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components.StepEditRow(step, projectID, "").Render(r.Context(), w)
}

func (h *StepHandler) UpdateStep(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	stepIDStr := chi.URLParam(r, "stepId")
	stepID, err := strconv.ParseInt(stepIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid step ID", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	script := r.FormValue("script_body")
	sortOrder, _ := strconv.ParseInt(r.FormValue("sort_order"), 10, 64)

	timeoutStr := strings.TrimSpace(r.FormValue("timeout_seconds"))
	var timeoutSeconds int64
	if timeoutStr != "" {
		t, err := strconv.ParseInt(timeoutStr, 10, 64)
		if err != nil || t < 0 {
			step := db.Step{
				ID:             stepID,
				ProjectID:      projectID,
				Name:           name,
				ScriptBody:     script,
				SortOrder:      sortOrder,
				TimeoutSeconds: timeoutSeconds,
			}
			WriteFormError(
				w,
				r,
				components.StepEditRow(
					step,
					projectID,
					"Timeout must be a non-negative integer",
				),
				components.StepEditRow(
					step,
					projectID,
					"Timeout must be a non-negative integer",
				),
			)
			return
		}
		timeoutSeconds = t
	}

	maxRetriesStr := strings.TrimSpace(r.FormValue("max_retries"))
	var maxRetries int64
	if maxRetriesStr != "" {
		m, err := strconv.ParseInt(maxRetriesStr, 10, 64)
		if err != nil || m < 0 {
			step := db.Step{
				ID:             stepID,
				ProjectID:      projectID,
				Name:           name,
				ScriptBody:     script,
				SortOrder:      sortOrder,
				TimeoutSeconds: timeoutSeconds,
				MaxRetries:     maxRetries,
			}
			WriteFormError(
				w,
				r,
				components.StepEditRow(
					step,
					projectID,
					"Max retries must be a non-negative integer",
				),
				components.StepEditRow(
					step,
					projectID,
					"Max retries must be a non-negative integer",
				),
			)
			return
		}
		maxRetries = m
	}

	if name == "" {
		step := db.Step{
			ID:             stepID,
			ProjectID:      projectID,
			Name:           name,
			ScriptBody:     script,
			SortOrder:      sortOrder,
			TimeoutSeconds: timeoutSeconds,
			MaxRetries:     maxRetries,
		}
		WriteFormError(
			w,
			r,
			components.StepEditRow(step, projectID, "Name is required"),
			components.StepEditRow(step, projectID, "Name is required"),
		)
		return
	}

	params := db.UpdateStepParams{
		ID:             stepID,
		Name:           name,
		ScriptBody:     script,
		SortOrder:      sortOrder,
		TimeoutSeconds: timeoutSeconds,
		MaxRetries:     maxRetries,
	}

	_, err = h.repo.Queries.UpdateStep(r.Context(), params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components.StepList(steps, projectID).Render(r.Context(), w)
}

func (h *StepHandler) DeleteStep(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	stepIDStr := chi.URLParam(r, "stepId")
	stepID, err := strconv.ParseInt(stepIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid step ID", http.StatusBadRequest)
		return
	}

	if err := h.repo.Queries.DeleteStep(r.Context(), stepID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		SetToastSuccess(w, "Step deleted")
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components.StepList(steps, projectID).Render(r.Context(), w)
}

func (h *StepHandler) ReorderStep(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	stepID, err := strconv.ParseInt(r.FormValue("step_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid step ID", http.StatusBadRequest)
		return
	}

	newOrder, err := strconv.ParseInt(r.FormValue("new_order"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid new order", http.StatusBadRequest)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var target db.Step
	found := false
	for _, s := range steps {
		if s.ID == stepID {
			target = s
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Step not found", http.StatusNotFound)
		return
	}

	oldOrder := target.SortOrder
	if oldOrder == newOrder {
		components.StepList(steps, projectID).Render(r.Context(), w)
		return
	}

	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	qtx := h.repo.Queries.WithTx(tx)

	if newOrder < oldOrder {
		for _, s := range steps {
			if s.ID == stepID {
				continue
			}
			if s.SortOrder >= newOrder && s.SortOrder < oldOrder {
				p := db.UpdateStepParams{
					ID:             s.ID,
					Name:           s.Name,
					ScriptBody:     s.ScriptBody,
					SortOrder:      s.SortOrder + 1,
					TimeoutSeconds: s.TimeoutSeconds,
					MaxRetries:     s.MaxRetries,
				}
				if _, err := qtx.UpdateStep(r.Context(), p); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
	} else {
		for _, s := range steps {
			if s.ID == stepID {
				continue
			}
			if s.SortOrder > oldOrder && s.SortOrder <= newOrder {
				p := db.UpdateStepParams{
					ID:             s.ID,
					Name:           s.Name,
					ScriptBody:     s.ScriptBody,
					SortOrder:      s.SortOrder - 1,
					TimeoutSeconds: s.TimeoutSeconds,
					MaxRetries:     s.MaxRetries,
				}
				if _, err := qtx.UpdateStep(r.Context(), p); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
	}

	_, err = qtx.UpdateStep(r.Context(), db.UpdateStepParams{
		ID:             target.ID,
		Name:           target.Name,
		ScriptBody:     target.ScriptBody,
		SortOrder:      newOrder,
		TimeoutSeconds: target.TimeoutSeconds,
		MaxRetries:     target.MaxRetries,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err = h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components.StepList(steps, projectID).Render(r.Context(), w)
}
