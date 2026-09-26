package api

import (
	"net/http"

	"durpdeploy/internal/db"
)

func (h *RunbookHandler) projectEnvironment(
	w http.ResponseWriter,
	r *http.Request,
	projectID, environmentID int64,
) (db.Project, bool) {
	if _, err := h.repo.Queries.GetEnvironment(
		r.Context(),
		environmentID,
	); err != nil {
		RespondError(w, http.StatusNotFound, "Environment not found")
		return db.Project{}, false
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "Project not found")
		return db.Project{}, false
	}
	if !project.LifecycleID.Valid {
		return project, true
	}
	stages, err := h.repo.Queries.ListLifecycleStages(
		r.Context(),
		project.LifecycleID.Int64,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot read lifecycle")
		return db.Project{}, false
	}
	for _, stage := range stages {
		if stage.EnvironmentID == environmentID {
			return project, true
		}
	}
	RespondError(w, http.StatusUnprocessableEntity,
		"Environment is not in project lifecycle")
	return db.Project{}, false
}
