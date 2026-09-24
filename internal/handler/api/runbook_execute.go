package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/gate"
	"durpdeploy/internal/repository"
)

type runbookExecuteRequest struct {
	EnvironmentID int64 `json:"environment_id"`
	VersionID     int64 `json:"version_id"`
}

// swagger:route POST /projects/{id}/runbooks/{runbookId}/executions runbooks executeRunbook
// Run one concrete version against a project environment.
func (h *RunbookHandler) Execute(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "runbookId")
	if !ok {
		return
	}
	var req runbookExecuteRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.EnvironmentID <= 0 || req.VersionID < 0 {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"Invalid environment or version",
		)
		return
	}
	project, ok := h.projectEnvironment(w, r, projectID, req.EnvironmentID)
	if !ok {
		return
	}
	requiresApproval, err := gate.RequiresApproval(
		r.Context(),
		h.repo,
		project,
		req.EnvironmentID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot check approval")
		return
	}
	status := "pending"
	if requiresApproval {
		status = "pending_approval"
	}
	user := auth.UserFromContext(r.Context())
	actor := sql.NullInt64{}
	if user != nil {
		actor = sql.NullInt64{Int64: user.ID, Valid: true}
	}
	execution, result, err := h.repo.CreateRunbookExecution(
		r.Context(), repository.RunbookExecutionRequest{
			ProjectID: projectID, RunbookID: id,
			VersionID: req.VersionID, EnvironmentID: req.EnvironmentID,
			ActorUserID: actor, Status: status,
		})
	if errors.Is(err, sql.ErrNoRows) {
		RespondError(w, http.StatusNotFound, "Runbook or version not found")
		return
	}
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot execute runbook",
		)
		return
	}
	if status == "pending" {
		go h.runner.Run(context.Background(), result.Deployment.ID,
			result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	}
	RespondJSON(w, http.StatusCreated, execution)
}
