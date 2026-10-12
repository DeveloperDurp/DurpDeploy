package handler

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func (h *VariableHandler) variableEnvironments(
	ctx context.Context,
	project db.Project,
) (pages.VariableEnvironments, error) {
	all, options, err := h.repo.VariableEnvironments(ctx, project)
	if err != nil {
		return pages.VariableEnvironments{}, err
	}
	local, err := h.repo.ListVariablesByProject(ctx, project.ID)
	if err != nil {
		return pages.VariableEnvironments{}, err
	}
	inherited, err := h.repo.InheritedVariables(ctx, project, local)
	return pages.VariableEnvironments{
		All:         all,
		Options:     options,
		Inheritance: inherited,
	}, err
}

func (h *VariableHandler) variableEnvironmentScope(
	w http.ResponseWriter,
	r *http.Request,
	projectID int64,
	requestedID string,
) (sql.NullInt64, bool) {
	var environmentID sql.NullInt64
	if requestedID != "" {
		id, err := strconv.ParseInt(requestedID, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid environment ID", http.StatusBadRequest)
			return environmentID, false
		}
		environmentID = sql.NullInt64{Int64: id, Valid: true}
	}
	allowed, err := h.repo.VariableEnvironmentAllowed(
		r.Context(),
		projectID,
		environmentID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return environmentID, false
	}
	if !allowed {
		http.Error(
			w,
			"Environment is not in project lifecycle",
			http.StatusUnprocessableEntity,
		)
		return environmentID, false
	}
	return environmentID, true
}
