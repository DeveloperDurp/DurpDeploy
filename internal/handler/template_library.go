package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
	"durpdeploy/views/pages"
)

type TemplateLibraryHandler struct {
	repo *repository.Repository
}

func NewTemplateLibraryHandler(
	repo *repository.Repository,
) *TemplateLibraryHandler {
	return &TemplateLibraryHandler{repo: repo}
}

func (h *TemplateLibraryHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	if projectID != 0 {
		if _, err := h.repo.Queries.GetProject(r.Context(), projectID); err != nil {
			status, message := LibraryTemplateError(err)
			http.Error(w, message, status)
			return
		}
	}
	entries := repository.TemplateLibrary()
	page := pages.TemplateLibraryPage(entries, projectID, r.URL.Path)
	if isFragmentRequest(r) {
		page = pages.TemplateLibraryContent(entries, projectID)
	}
	if err := page.Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Could not render template library",
			http.StatusInternalServerError,
		)
	}
}

func (h *TemplateLibraryHandler) Preview(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.render(w, r, repository.ApplyLibraryTemplate{}, "", http.StatusOK)
}

func (h *TemplateLibraryHandler) render(
	w http.ResponseWriter,
	r *http.Request,
	input repository.ApplyLibraryTemplate,
	message string,
	status int,
) {
	entry, err := repository.GetLibraryTemplate(chi.URLParam(r, "slug"))
	if err != nil {
		statusCode, errorMessage := LibraryTemplateError(err)
		http.Error(w, errorMessage, statusCode)
		return
	}
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	data := pages.LibraryTemplateFormData{
		Entry:     entry,
		ProjectID: projectID,
		Input:     input,
		Error:     message,
	}
	if projectID != 0 {
		data.Project, err = h.repo.Queries.GetProject(r.Context(), projectID)
	} else {
		data.Projects, err = h.destinationProjects(r)
	}
	if err != nil {
		statusCode, errorMessage := LibraryTemplateError(err)
		http.Error(w, errorMessage, statusCode)
		return
	}
	data.Labels, err = h.repo.Queries.ListAvailableAgentLabels(r.Context())
	if err != nil {
		http.Error(
			w,
			"Could not list agent labels",
			http.StatusInternalServerError,
		)
		return
	}
	w.WriteHeader(status)
	if err := pages.LibraryTemplatePage(data, r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Could not render template preview",
			http.StatusInternalServerError,
		)
	}
}

func (h *TemplateLibraryHandler) destinationProjects(
	r *http.Request,
) ([]db.Project, error) {
	result := []db.Project{}
	if !auth.CanAuthor(r.Context()) {
		return result, nil
	}
	projects, err := (&ProjectHandler{repo: h.repo}).projectsForUser(r)
	if err != nil {
		return nil, err
	}
	user := auth.UserFromContext(r.Context())
	for _, project := range projects {
		if user.Role != "admin" {
			member, err := h.repo.Queries.GetProjectMember(
				r.Context(),
				db.GetProjectMemberParams{
					UserID:    user.ID,
					ProjectID: project.ID,
				},
			)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !roles.AtLeast(member.Role, "maintainer") {
				continue
			}
		}
		result = append(result, project)
	}
	return result, nil
}

func (h *TemplateLibraryHandler) Apply(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid template form", http.StatusBadRequest)
		return
	}
	entry, err := repository.GetLibraryTemplate(chi.URLParam(r, "slug"))
	if err != nil {
		status, message := LibraryTemplateError(err)
		http.Error(w, message, status)
		return
	}
	input := repository.ApplyLibraryTemplate{
		Version:        r.PostFormValue("version"),
		Reviewed:       r.PostFormValue("reviewed") == "true",
		VariableValues: map[string]string{},
	}
	if labels := strings.TrimSpace(r.PostFormValue("agent_selectors")); labels != "" {
		input.AgentSelectors = strings.Split(labels, ",")
	}
	for _, variable := range entry.Snapshot.Variables {
		input.VariableValues[variable.Key] = r.PostFormValue(
			"variable_" + variable.Key,
		)
	}
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	result, err := h.repo.ApplyLibraryTemplate(
		r.Context(),
		ProjectTemplateActor(r),
		projectID,
		entry.Slug,
		input,
	)
	if err != nil {
		// Never reflect fresh secrets into HTML after a rejected application.
		for _, variable := range entry.Snapshot.Variables {
			if variable.Secret {
				delete(input.VariableValues, variable.Key)
			}
		}
		input.Reviewed = false
		status, message := LibraryTemplateError(err)
		h.render(w, r, input, message, status)
		return
	}
	audit.SetLibraryTemplateResult(r, result)
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d", projectID),
		http.StatusSeeOther,
	)
}

func LibraryTemplateError(err error) (int, string) {
	switch {
	case errors.Is(err, repository.ErrLibraryConflict):
		return http.StatusConflict, err.Error()
	case IsUniqueViolation(err):
		return http.StatusConflict, "Template variables conflict with project configuration; no changes were made"
	case errors.Is(err, repository.ErrProjectTemplateInvalid):
		return http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, repository.ErrProjectTemplateAccess):
		return http.StatusForbidden, "Template application requires Maintainer permission"
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "Template or project not found"
	default:
		return http.StatusInternalServerError, "Could not apply template"
	}
}
