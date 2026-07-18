package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

// Variable resolution rule (documented for future use):
// At deploy time, query release_variables WHERE release_id = deployment's release.
// For each variable: if environment_id matches deployment environment, use that value;
// else if unscoped (NULL), use that value; else use empty string.

type VariableHandler struct {
	repo *repository.Repository
}

func NewVariableHandler(repo *repository.Repository) *VariableHandler {
	return &VariableHandler{repo: repo}
}

func (h *VariableHandler) ListVariables(
	w http.ResponseWriter,
	r *http.Request,
) {
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

	variables, err := h.repo.ListVariablesByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		pages.VariablesFragment(project, variables, environments, "").
			Render(r.Context(), w)
	} else {
		pages.VariablesPage(project, variables, environments, "", r.URL.Path).
			Render(r.Context(), w)
	}
}

func (h *VariableHandler) CreateVariable(
	w http.ResponseWriter,
	r *http.Request,
) {
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
	value := r.FormValue("value")
	envIDStr := r.FormValue("environment_id")
	var secret int64
	if r.FormValue("secret") == "on" {
		secret = 1
	}

	if name == "" {
		environments, _ := h.repo.Queries.ListEnvironments(r.Context())
		project, _ := h.repo.Queries.GetProject(r.Context(), projectID)
		variables, _ := h.repo.ListVariablesByProject(
			r.Context(),
			projectID,
		)
		WriteFormError(
			w,
			r,
			pages.VariableForm(projectID, environments, "Name is required"),
			pages.VariablesPage(
				project,
				variables,
				environments,
				"Name is required",
				r.URL.Path,
			),
		)
		return
	}

	var envID sql.NullInt64
	if envIDStr != "" {
		id, err := strconv.ParseInt(envIDStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid environment ID", http.StatusBadRequest)
			return
		}
		envID = sql.NullInt64{Int64: id, Valid: true}
	}

	params := db.CreateVariableParams{
		ProjectID:     projectID,
		Name:          name,
		Value:         sql.NullString{String: value, Valid: value != ""},
		EnvironmentID: envID,
		Secret:        secret,
	}

	_, err = h.repo.CreateVariable(r.Context(), params)
	if err != nil {
		if IsUniqueViolation(err) {
			environments, _ := h.repo.Queries.ListEnvironments(r.Context())
			project, _ := h.repo.Queries.GetProject(r.Context(), projectID)
			variables, _ := h.repo.ListVariablesByProject(
				r.Context(),
				projectID,
			)
			WriteFormError(
				w,
				r,
				pages.VariableForm(
					projectID,
					environments,
					"A variable with this name and scope already exists",
				),
				pages.VariablesPage(
					project,
					variables,
					environments,
					"A variable with this name and scope already exists",
					r.URL.Path,
				),
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		h.renderVariablesFragment(w, r, projectID)
	} else {
		http.Redirect(
			w,
			r,
			fmt.Sprintf("/projects/%d/variables", projectID),
			http.StatusSeeOther,
		)
	}
}

func (h *VariableHandler) EditVariable(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	varIDStr := chi.URLParam(r, "varId")
	varID, err := strconv.ParseInt(varIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid variable ID", http.StatusBadRequest)
		return
	}

	variable, err := h.repo.GetVariable(r.Context(), varID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pages.VariableEditRow(projectID, variable, environments, "").
		Render(r.Context(), w)
}

func (h *VariableHandler) UpdateVariable(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	varIDStr := chi.URLParam(r, "varId")
	varID, err := strconv.ParseInt(varIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid variable ID", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	value := r.FormValue("value")
	envIDStr := r.FormValue("environment_id")
	var secret int64
	if r.FormValue("secret") == "on" {
		secret = 1
	}

	if name == "" {
		variable := db.Variable{
			ID:        varID,
			ProjectID: projectID,
			Name:      name,
			Value:     sql.NullString{String: value, Valid: value != ""},
		}
		environments, _ := h.repo.Queries.ListEnvironments(r.Context())
		if envIDStr != "" {
			id, _ := strconv.ParseInt(envIDStr, 10, 64)
			variable.EnvironmentID = sql.NullInt64{Int64: id, Valid: true}
		}
		project, _ := h.repo.Queries.GetProject(r.Context(), projectID)
		variables, _ := h.repo.ListVariablesByProject(
			r.Context(),
			projectID,
		)
		WriteFormError(
			w,
			r,
			pages.VariableEditRow(
				projectID,
				variable,
				environments,
				"Name is required",
			),
			pages.VariablesPage(
				project,
				variables,
				environments,
				"Name is required",
				r.URL.Path,
			),
		)
		return
	}

	var envID sql.NullInt64
	if envIDStr != "" {
		id, err := strconv.ParseInt(envIDStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid environment ID", http.StatusBadRequest)
			return
		}
		envID = sql.NullInt64{Int64: id, Valid: true}
	}

	params := db.UpdateVariableParams{
		ID:            varID,
		Name:          name,
		Value:         sql.NullString{String: value, Valid: value != ""},
		EnvironmentID: envID,
		Secret:        secret,
	}

	_, err = h.repo.UpdateVariable(r.Context(), params)
	if err != nil {
		if IsUniqueViolation(err) {
			variable := db.Variable{
				ID:        varID,
				ProjectID: projectID,
				Name:      name,
				Value: sql.NullString{
					String: value,
					Valid:  value != "",
				},
				EnvironmentID: envID,
			}
			environments, _ := h.repo.Queries.ListEnvironments(r.Context())
			project, _ := h.repo.Queries.GetProject(r.Context(), projectID)
			variables, _ := h.repo.ListVariablesByProject(
				r.Context(),
				projectID,
			)
			WriteFormError(
				w,
				r,
				pages.VariableEditRow(
					projectID,
					variable,
					environments,
					"A variable with this name and scope already exists",
				),
				pages.VariablesPage(
					project,
					variables,
					environments,
					"A variable with this name and scope already exists",
					r.URL.Path,
				),
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		h.renderVariablesFragment(w, r, projectID)
	} else {
		http.Redirect(
			w,
			r,
			fmt.Sprintf("/projects/%d/variables", projectID),
			http.StatusSeeOther,
		)
	}
}

func (h *VariableHandler) DeleteVariable(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	varIDStr := chi.URLParam(r, "varId")
	varID, err := strconv.ParseInt(varIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid variable ID", http.StatusBadRequest)
		return
	}

	if err := h.repo.Queries.DeleteVariable(r.Context(), varID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderVariablesFragment(w, r, projectID)
}

func (h *VariableHandler) renderVariablesFragment(
	w http.ResponseWriter,
	r *http.Request,
	projectID int64,
) {
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	variables, err := h.repo.ListVariablesByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pages.VariablesFragment(project, variables, environments, "").
		Render(r.Context(), w)
}
