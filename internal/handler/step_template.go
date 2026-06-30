package handler

import (
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

func (h *StepTemplateHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.repo.Queries.ListStepTemplates(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.TemplatesListContent(templates).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.TemplatesList(templates, r.URL.Path).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (h *StepTemplateHandler) NewTemplateForm(w http.ResponseWriter, r *http.Request) {
	tpl := &db.StepTemplate{}
	if err := pages.TemplateForm(tpl, true, "", r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepTemplateHandler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	script := r.FormValue("script_body")

	if name == "" {
		tpl := &db.StepTemplate{Name: name, ScriptBody: script}
		WriteFormError(w, r, pages.TemplateFormFragment(tpl, true, "Name is required"), pages.TemplateForm(tpl, true, "Name is required", r.URL.Path))
		return
	}

	params := db.CreateStepTemplateParams{
		Name:       name,
		ScriptBody: script,
	}

	if _, err := h.repo.Queries.CreateStepTemplate(r.Context(), params); err != nil {
		if IsUniqueViolation(err) {
			tpl := &db.StepTemplate{Name: name, ScriptBody: script}
			WriteFormError(w, r, pages.TemplateFormFragment(tpl, true, "A template with this name already exists"), pages.TemplateForm(tpl, true, "A template with this name already exists", r.URL.Path))
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (h *StepTemplateHandler) EditTemplateForm(w http.ResponseWriter, r *http.Request) {
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

	if err := pages.TemplateForm(&tpl, false, "", r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepTemplateHandler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
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
		WriteFormError(w, r, pages.TemplateFormFragment(tpl, false, "Name is required"), pages.TemplateForm(tpl, false, "Name is required", r.URL.Path))
		return
	}

	params := db.UpdateStepTemplateParams{
		ID:         id,
		Name:       name,
		ScriptBody: script,
	}

	if _, err := h.repo.Queries.UpdateStepTemplate(r.Context(), params); err != nil {
		if IsUniqueViolation(err) {
			tpl := &db.StepTemplate{ID: id, Name: name, ScriptBody: script}
			WriteFormError(w, r, pages.TemplateFormFragment(tpl, false, "A template with this name already exists"), pages.TemplateForm(tpl, false, "A template with this name already exists", r.URL.Path))
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (h *StepTemplateHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
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

func (h *StepTemplateHandler) InsertTemplate(w http.ResponseWriter, r *http.Request) {
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
	}

	if _, err := h.repo.Queries.CreateStep(r.Context(), params); err != nil {
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

func (h *StepTemplateHandler) SaveStepAsTemplate(w http.ResponseWriter, r *http.Request) {
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

	params := db.CreateStepTemplateParams{
		Name:       step.Name,
		ScriptBody: step.ScriptBody,
	}

	if _, err := h.repo.Queries.CreateStepTemplate(r.Context(), params); err != nil {
		if IsUniqueViolation(err) {
			if r.Header.Get("HX-Request") == "true" {
				SetToastError(w, "A template with this name already exists")
				w.WriteHeader(http.StatusConflict)
				steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
				return
			}
			http.Error(w, "A template with this name already exists", http.StatusConflict)
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
		if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (h *StepTemplateHandler) TemplatesPicker(w http.ResponseWriter, r *http.Request) {
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
