package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
	"durpdeploy/internal/repository"
)

// swagger:route GET /projects/{id}/runbook-executions runbooks listRunbookExecutions
// List runbook execution history.
func (h *RunbookHandler) ListExecutions(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	items, err := h.repo.Queries.ListRunbookExecutions(r.Context(), projectID)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot list runbook executions",
		)
		return
	}
	RespondJSON(w, http.StatusOK, items)
}

func (h *RunbookHandler) execution(
	w http.ResponseWriter,
	r *http.Request,
) (db.GetRunbookExecutionRow, bool) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return db.GetRunbookExecutionRow{}, false
	}
	id, ok := parseIDParam(w, r, "executionId")
	if !ok {
		return db.GetRunbookExecutionRow{}, false
	}
	execution, err := h.repo.Queries.GetRunbookExecution(
		r.Context(),
		db.GetRunbookExecutionParams{
			ID: id, ProjectID: projectID,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		RespondError(w, http.StatusNotFound, "Runbook execution not found")
		return db.GetRunbookExecutionRow{}, false
	}
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot read runbook execution",
		)
		return db.GetRunbookExecutionRow{}, false
	}
	return execution, true
}

// swagger:route GET /projects/{id}/runbook-executions/{executionId} runbooks getRunbookExecution
// Read a runbook execution.
func (h *RunbookHandler) GetExecution(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if ok {
		RespondJSON(w, http.StatusOK, execution)
	}
}

// swagger:route GET /projects/{id}/runbook-executions/{executionId}/logs runbooks listRunbookLogs
// Read persisted runbook logs in execution order.
func (h *RunbookHandler) Logs(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	logs, err := h.repo.ListRunbookLogs(
		r.Context(),
		execution.DeploymentID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot read logs")
		return
	}
	RespondJSON(w, http.StatusOK, logs)
}

// swagger:route GET /projects/{id}/runbook-executions/{executionId}/logs/stream runbooks streamRunbookLogs
// Stream persisted and live runbook logs.
func (h *RunbookHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if ok {
		NewLogHandler(h.runner.Broker(), h.repo).streamDeploymentLogs(
			w, r, execution.DeploymentID,
		)
	}
}

// swagger:route POST /projects/{id}/runbook-executions/{executionId}/cancel runbooks cancelRunbookExecution
// Cancel a running runbook execution.
func (h *RunbookHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	dep, err := h.repo.Queries.GetDeployment(
		r.Context(),
		execution.DeploymentID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot read execution")
		return
	}
	if dep.AssignedAgentID.Valid {
		err = h.repo.CancelAssignedRemoteDeployment(r.Context(),
			repository.RemoteAssignedDeployment{
				DeploymentID: dep.ID, AgentID: dep.AssignedAgentID.String,
			})
	} else if dep.Status == "running" {
		err = h.runner.Cancel(dep.ID)
	} else {
		RespondError(w, http.StatusConflict, "Execution is not running")
		return
	}
	if err != nil {
		RespondError(w, http.StatusConflict, "Cannot cancel execution")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// swagger:route POST /projects/{id}/runbook-executions/{executionId}/approve runbooks approveRunbookExecution
// Approve and start a pending runbook execution.
func (h *RunbookHandler) Approve(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Role != "admin" {
		RespondError(w, http.StatusForbidden, "Admin access required")
		return
	}
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	result, err := h.repo.ApproveDeployment(
		r.Context(),
		db.CreateApprovalParams{
			DeploymentID: execution.DeploymentID,
			ApprovedBy:   user.Name,
			ApproverUserID: sql.NullInt64{
				Int64: user.ID, Valid: true,
			},
			RequiredApproverRole: "admin",
		},
	)
	if errors.Is(err, repository.ErrDeploymentApprovalConflict) {
		RespondError(
			w,
			http.StatusConflict,
			"Execution is not pending approval",
		)
		return
	}
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot approve execution",
		)
		return
	}
	go h.runner.Run(context.Background(), result.Deployment.ID,
		result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	RespondJSON(w, http.StatusOK, map[string]string{"status": "pending"})
}

// swagger:route POST /projects/{id}/runbook-executions/{executionId}/retry runbooks retryRunbookExecution
// Retry the pinned version of a completed runbook execution.
func (h *RunbookHandler) Retry(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	if execution.Status != "failed" && execution.Status != "cancelled" &&
		execution.Status != "succeeded" {
		RespondError(w, http.StatusConflict, "Execution is not complete")
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), execution.ProjectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot read project")
		return
	}
	requiresApproval, err := gate.RequiresApproval(
		r.Context(),
		h.repo,
		project,
		execution.EnvironmentID,
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
	retried, result, err := h.repo.CreateRunbookExecution(
		r.Context(), repository.RunbookExecutionRequest{
			ProjectID: execution.ProjectID, RunbookID: execution.RunbookID,
			VersionID:     execution.RunbookVersionID,
			EnvironmentID: execution.EnvironmentID,
			ActorUserID:   actor, Status: status,
		})
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot retry execution",
		)
		return
	}
	if status == "pending" {
		go h.runner.Run(context.Background(), result.Deployment.ID,
			result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	}
	RespondJSON(w, http.StatusCreated, retried)
}
