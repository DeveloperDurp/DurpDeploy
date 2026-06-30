package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/components"
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

	if name == "" {
		step := db.Step{ProjectID: projectID, Name: name, ScriptBody: script}
		WriteFormError(w, r, components.StepForm(step, projectID, true, "Name is required"), components.StepForm(step, projectID, true, "Name is required"))
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
		ProjectID:  projectID,
		Name:       name,
		ScriptBody: script,
		SortOrder:  sortOrder,
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

	if name == "" {
		step := db.Step{ID: stepID, ProjectID: projectID, Name: name, ScriptBody: script, SortOrder: sortOrder}
		WriteFormError(w, r, components.StepEditRow(step, projectID, "Name is required"), components.StepEditRow(step, projectID, "Name is required"))
		return
	}

	params := db.UpdateStepParams{
		ID:         stepID,
		Name:       name,
		ScriptBody: script,
		SortOrder:  sortOrder,
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
					ID:         s.ID,
					Name:       s.Name,
					ScriptBody: s.ScriptBody,
					SortOrder:  s.SortOrder + 1,
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
					ID:         s.ID,
					Name:       s.Name,
					ScriptBody: s.ScriptBody,
					SortOrder:  s.SortOrder - 1,
				}
				if _, err := qtx.UpdateStep(r.Context(), p); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
	}

	_, err = qtx.UpdateStep(r.Context(), db.UpdateStepParams{
		ID:         target.ID,
		Name:       target.Name,
		ScriptBody: target.ScriptBody,
		SortOrder:  newOrder,
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
