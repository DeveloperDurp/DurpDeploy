package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/repository"
)

// ServeAvailableVariables is shared by the authenticated web and bearer APIs.
// Both routes must be inside RequireProjectAccess, never on a public group.
func ServeAvailableVariables(
	repo *repository.Repository, w http.ResponseWriter, r *http.Request,
) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	projectID, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		writeCatalogError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}
	project, err := repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		writeCatalogReadError(w, err)
		return
	}
	preview, err := variablePreviewContext(repo, r, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeCatalogError(w, http.StatusNotFound, "Step not found")
		} else {
			writeCatalogError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	allowed, err := repo.VariableEnvironmentAllowed(
		r.Context(),
		projectID,
		sql.NullInt64{
			Int64: preview.EnvironmentID,
			Valid: preview.EnvironmentID != 0,
		},
	)
	if err != nil {
		writeCatalogReadError(w, err)
		return
	}
	if !allowed {
		writeCatalogError(w, http.StatusUnprocessableEntity,
			"Environment is not in project lifecycle")
		return
	}
	if preview.EnvironmentID != 0 {
		if _, err := repo.Queries.GetEnvironment(
			r.Context(),
			preview.EnvironmentID,
		); err != nil {
			writeCatalogReadError(w, err)
			return
		}
	}
	catalog, err := repo.AvailableVariables(r.Context(), project, preview)
	if err != nil {
		writeCatalogReadError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(catalog)
}

func (h *VariableHandler) Available(w http.ResponseWriter, r *http.Request) {
	ServeAvailableVariables(h.repo, w, r)
}

func variablePreviewContext(
	repo *repository.Repository, r *http.Request, projectID int64,
) (repository.VariableContext, error) {
	query, err := variablePreviewQuery(r.URL.RawQuery)
	if err != nil {
		return repository.VariableContext{}, err
	}
	preview, err := savedVariablePreview(
		repo,
		r,
		projectID,
		query.Get("step_id"),
	)
	if err != nil {
		return preview, err
	}
	if query.Get("environment_id") != "" {
		preview.EnvironmentID, err = positivePreviewID(
			query.Get("environment_id"),
			"environment_id",
		)
		if err != nil {
			return preview, err
		}
	}
	if value, exists := query["variable_names"]; exists {
		preview.VariableNames = parseStepVariableNames(value[0])
	}
	if value, exists := query["interpreter"]; exists {
		preview.Interpreter = value[0]
	}
	if value, exists := query["execution_target"]; exists {
		preview.Target = value[0]
	}
	if value, exists := query["agent_execution_mode"]; exists {
		preview.AgentMode = value[0]
	}
	return validateVariablePreview(preview)
}

func variablePreviewQuery(raw string) (url.Values, error) {
	if len(raw) > 16384 {
		return nil, errors.New("variable context is too large")
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errors.New("invalid variable context query")
	}
	for _, values := range query {
		if len(values) != 1 {
			return nil, errors.New("duplicate variable context parameter")
		}
	}
	return query, nil
}

func savedVariablePreview(
	repo *repository.Repository,
	r *http.Request,
	projectID int64,
	stepID string,
) (repository.VariableContext, error) {
	preview := repository.VariableContext{
		Interpreter: interpreter.Bash, Target: "local", AgentMode: "host",
		VariableNames: []string{},
	}
	if stepID == "" {
		return preview, nil
	}
	id, err := positivePreviewID(stepID, "step_id")
	if err != nil {
		return preview, err
	}
	step, err := repo.Queries.GetStep(r.Context(), id)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return preview, errors.New("could not read step context")
		}
		return preview, err
	}
	if step.ProjectID != projectID {
		return preview, sql.ErrNoRows
	}
	preview.Interpreter, preview.Target = step.Interpreter, step.ExecutionTarget
	preview.AgentMode = step.AgentExecutionMode
	if step.VariableNames != "" {
		if err := json.Unmarshal(
			[]byte(step.VariableNames), &preview.VariableNames,
		); err != nil {
			return preview, errors.New("invalid saved variable selection")
		}
	}
	return preview, nil
}

func validateVariablePreview(
	preview repository.VariableContext,
) (repository.VariableContext, error) {
	for _, name := range preview.VariableNames {
		if len(name) > 255 ||
			containerenv.ValidateName(
				name,
				false,
			) == containerenv.ErrIdentifier {
			return preview, repository.ErrVariableName
		}
	}
	var err error
	preview.Interpreter, err = interpreter.Validate(preview.Interpreter)
	if err != nil {
		return preview, err
	}
	if preview.AgentMode == "" {
		preview.AgentMode = "host"
	}
	if (preview.Target != "local" && preview.Target != "agent") ||
		(preview.AgentMode != "host" && preview.AgentMode != "container") ||
		(preview.Target == "local" && preview.AgentMode != "host") {
		return preview, errors.New(
			"invalid execution target or agent execution mode",
		)
	}
	if preview.VariableNames == nil {
		preview.VariableNames = []string{}
	}
	return preview, nil
}

func positivePreviewID(value, name string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return id, nil
}

func writeCatalogError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).
		Encode(map[string]string{"error": strings.TrimSpace(message)})
}

func writeCatalogReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeCatalogError(w, http.StatusNotFound, "Variable context not found")
		return
	}
	writeCatalogError(
		w,
		http.StatusInternalServerError,
		"Could not read available variables",
	)
}
