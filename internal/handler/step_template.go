package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/components"
	"durpdeploy/views/pages"
)

type StepTemplateHandler struct {
	repo *repository.Repository
}

func NewStepTemplateHandler(repo *repository.Repository) *StepTemplateHandler {
	return &StepTemplateHandler{repo: repo}
}

func (h *StepTemplateHandler) ListTemplates(
	w http.ResponseWriter,
	r *http.Request,
) {
	templates, err := h.repo.Queries.ListStepTemplates(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.TemplatesListContent(templates).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.TemplatesList(templates, r.URL.Path).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (h *StepTemplateHandler) NewTemplateForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	tpl := &db.StepTemplate{}
	if err := pages.TemplateForm(tpl, true, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepTemplateHandler) CreateTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	script := r.FormValue("script_body")

	if name == "" {
		tpl := &db.StepTemplate{Name: name, ScriptBody: script}
		WriteFormError(
			w,
			r,
			pages.TemplateFormFragment(tpl, true, "Name is required"),
			pages.TemplateForm(tpl, true, "Name is required", r.URL.Path),
		)
		return
	}

	params := db.CreateStepTemplateParams{
		Name:       name,
		ScriptBody: script,
	}

	_, err := h.repo.CreateStepTemplateWithPlacement(
		r.Context(),
		params,
		"local",
		nil,
	)
	if err != nil {
		if IsUniqueViolation(err) {
			tplErr := &db.StepTemplate{Name: name, ScriptBody: script}
			WriteFormError(
				w,
				r,
				pages.TemplateFormFragment(
					tplErr,
					true,
					"A template with this name already exists",
				),
				pages.TemplateForm(
					tplErr,
					true,
					"A template with this name already exists",
					r.URL.Path,
				),
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (h *StepTemplateHandler) EditTemplateForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid template ID", http.StatusBadRequest)
		return
	}

	tpl, err := h.repo.Queries.GetStepTemplate(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.TemplateForm(&tpl, false, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepTemplateHandler) UpdateTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid template ID", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	script := r.FormValue("script_body")

	if name == "" {
		tpl := &db.StepTemplate{ID: id, Name: name, ScriptBody: script}
		WriteFormError(
			w,
			r,
			pages.TemplateFormFragment(tpl, false, "Name is required"),
			pages.TemplateForm(tpl, false, "Name is required", r.URL.Path),
		)
		return
	}

	params := db.UpdateStepTemplateParams{
		ID:         id,
		Name:       name,
		ScriptBody: script,
	}

	existing, err := h.repo.Queries.GetStepTemplate(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	selectors, err := h.repo.Queries.ListTemplateAgentSelectors(
		r.Context(),
		id,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, err = h.repo.UpdateStepTemplateWithPlacement(
		r.Context(),
		params,
		existing.ExecutionTarget,
		selectors,
	)
	if err != nil {
		if IsUniqueViolation(err) {
			tpl := &db.StepTemplate{ID: id, Name: name, ScriptBody: script}
			WriteFormError(
				w,
				r,
				pages.TemplateFormFragment(
					tpl,
					false,
					"A template with this name already exists",
				),
				pages.TemplateForm(
					tpl,
					false,
					"A template with this name already exists",
					r.URL.Path,
				),
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (h *StepTemplateHandler) DeleteTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid template ID", http.StatusBadRequest)
		return
	}

	if err := h.repo.Queries.DeleteStepTemplate(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (h *StepTemplateHandler) ListTemplateHistory(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid template ID", http.StatusBadRequest)
		return
	}

	tpl, err := h.repo.Queries.GetStepTemplate(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(
				w,
				"Template not found",
				http.StatusNotFound,
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	versions, err := h.repo.Queries.ListStepTemplateVersions(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.TemplateHistoryContent(tpl, versions).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if err := pages.TemplateHistoryPage(tpl, versions, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepTemplateHandler) InsertTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	templateIDStr := chi.URLParam(r, "templateId")
	templateID, err := strconv.ParseInt(templateIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid template ID", http.StatusBadRequest)
		return
	}

	tpl, err := h.repo.Queries.GetStepTemplate(r.Context(), templateID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var sortOrder int64
	for _, s := range steps {
		if s.SortOrder >= sortOrder {
			sortOrder = s.SortOrder + 1
		}
	}

	params := db.CreateStepParams{
		ProjectID:  projectID,
		Name:       tpl.Name,
		ScriptBody: tpl.ScriptBody,
		SortOrder:  sortOrder,
		// ponytail: StepTemplate has no timeout or max_retries field yet;
		// new step inherits defaults (0/0).
	}

	selectors, err := h.repo.Queries.ListTemplateAgentSelectors(
		r.Context(),
		templateID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.repo.CreateStepWithPlacement(
		r.Context(),
		params,
		tpl.ExecutionTarget,
		selectors,
	); err != nil {
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

func (h *StepTemplateHandler) SaveStepAsTemplate(
	w http.ResponseWriter,
	r *http.Request,
) {
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

	step, err := (&StepHandler{repo: h.repo}).getProjectStep(
		r,
		projectID,
		stepID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if step.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}

	params := db.CreateStepTemplateParams{
		Name:       step.Name,
		ScriptBody: step.ScriptBody,
	}
	selectors, err := h.repo.Queries.ListStepAgentSelectors(
		r.Context(),
		step.ID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := h.repo.CreateStepTemplateWithPlacement(
		r.Context(),
		params,
		step.ExecutionTarget,
		selectors,
	); err != nil {
		if IsUniqueViolation(err) {
			if r.Header.Get("HX-Request") == "true" {
				SetToastError(w, "A template with this name already exists")
				w.WriteHeader(http.StatusConflict)
				steps, err := h.repo.Queries.ListStepsByProject(
					r.Context(),
					projectID,
				)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if err := components.StepList(steps, projectID).
					Render(r.Context(), w); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
				return
			}
			http.Error(
				w,
				"A template with this name already exists",
				http.StatusConflict,
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		SetToastSuccess(w, "Step saved as template")
		steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := components.StepList(steps, projectID).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (h *StepTemplateHandler) TemplatesPicker(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	templates, err := h.repo.Queries.ListStepTemplates(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components.TemplatePicker(templates, projectID).Render(r.Context(), w)
}
