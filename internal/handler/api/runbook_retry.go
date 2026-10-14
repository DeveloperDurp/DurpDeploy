package api

import (
	"context"
	"database/sql"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/repository"
	"errors"
	"net/http"
)

// swagger:route POST /projects/{id}/runbook-executions/{executionId}/retry runbooks retryRunbookExecution
// Retry the pinned version of a completed runbook execution.
//
// Responses:
// 400: body:BadRequestError
// 413: body:RequestEntityTooLargeError
// 201: body:RunbookExecution
// 404: body:NotFoundError
// 409: body:ConflictError
func (h *RunbookHandler) Retry(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	if execution.Status != "failed" && execution.Status != "cancelled" &&
		execution.Status != "succeeded" && execution.Status != "cleanup_unconfirmed" {
		RespondError(w, http.StatusConflict, "Execution is not complete")
		return
	}
	user := auth.UserFromContext(r.Context())
	actor := sql.NullInt64{}
	if user != nil {
		actor = sql.NullInt64{Int64: user.ID, Valid: true}
	}
	retried, result, err := h.repo.CreateRunbookExecution(
		r.Context(), repository.RunbookExecutionRequest{
			ProjectID: execution.ProjectID, RunbookID: execution.RunbookID,
			VersionID:               execution.RunbookVersionID,
			EnvironmentID:           execution.EnvironmentID,
			ActorUserID:             actor,
			RetrySourceDeploymentID: execution.DeploymentID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		RespondError(w, http.StatusNotFound, "Runbook not found")
		return
	}
	if errors.Is(err, repository.ErrRunbookRemoteOutcomeUnconfirmed) ||
		errors.Is(err, repository.ErrContainerCleanupUnconfirmed) {
		RespondError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, repository.ErrRunbookGate) {
		RespondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if writeArtifactError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrLegacyServerStep) {
		RespondError(
			w,
			http.StatusConflict,
			repository.ErrLegacyServerStep.Error(),
		)
		return
	}
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot retry execution",
		)
		return
	}
	if result.Deployment.Status == "pending" {
		go h.runner.Run(context.Background(), result.Deployment.ID,
			result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	}
	RespondJSON(w, http.StatusCreated, retried)
}
