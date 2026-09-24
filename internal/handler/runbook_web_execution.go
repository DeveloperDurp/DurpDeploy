package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

func (h *RunbookHandler) execution(
	w http.ResponseWriter,
	r *http.Request,
) (db.GetRunbookExecutionRow, bool) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", http.StatusBadRequest)
		return db.GetRunbookExecutionRow{}, false
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "executionId"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid execution", http.StatusBadRequest)
		return db.GetRunbookExecutionRow{}, false
	}
	execution, err := h.repo.Queries.GetRunbookExecution(r.Context(),
		db.GetRunbookExecutionParams{ID: id, ProjectID: projectID})
	if err != nil {
		http.NotFound(w, r)
		return db.GetRunbookExecutionRow{}, false
	}
	return execution, true
}

func (h *RunbookHandler) Execution(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), execution.ProjectID)
	if err != nil {
		http.Error(w, "Cannot read project", http.StatusInternalServerError)
		return
	}
	book, err := h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
		ID: execution.RunbookID, ProjectID: execution.ProjectID,
	})
	if err != nil {
		http.Error(w, "Cannot read runbook", http.StatusInternalServerError)
		return
	}
	logs, err := h.repo.ListRunbookLogs(
		r.Context(),
		execution.DeploymentID,
	)
	if err != nil {
		http.Error(w, "Cannot read logs", http.StatusInternalServerError)
		return
	}
	if err := pages.RunbookExecutionPage(project, book, execution, logs, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render execution", http.StatusInternalServerError)
	}
}

func (h *RunbookHandler) Status(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if ok {
		_ = pages.RunbookExecutionStatus(execution.ProjectID, execution).
			Render(r.Context(), w)
	}
}

func (h *RunbookHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if ok {
		NewLogHandler(h.runner.Broker(), h.repo).streamDeploymentLogs(
			w, r, execution.DeploymentID)
	}
}

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
		http.Error(w, "Cannot read execution", http.StatusInternalServerError)
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
		http.Error(w, "Execution is not running", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Cannot cancel execution", http.StatusConflict)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%d/runbooks/executions/%d",
		execution.ProjectID, execution.ID), http.StatusSeeOther)
}

func (h *RunbookHandler) Approve(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Role != "admin" {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	result, err := h.repo.ApproveDeployment(
		r.Context(),
		db.CreateApprovalParams{
			DeploymentID: execution.DeploymentID, ApprovedBy: user.Name,
			ApproverUserID:       sql.NullInt64{Int64: user.ID, Valid: true},
			RequiredApproverRole: "admin",
		},
	)
	if err != nil {
		http.Error(w, "Cannot approve execution", http.StatusConflict)
		return
	}
	go h.runner.Run(context.Background(), result.Deployment.ID,
		result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	http.Redirect(w, r, fmt.Sprintf("/projects/%d/runbooks/executions/%d",
		execution.ProjectID, execution.ID), http.StatusSeeOther)
}

func (h *RunbookHandler) Retry(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if !ok {
		return
	}
	if execution.Status != "failed" && execution.Status != "cancelled" &&
		execution.Status != "succeeded" {
		http.Error(w, "Execution is not complete", http.StatusConflict)
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), execution.ProjectID)
	if err != nil {
		http.Error(w, "Cannot read project", http.StatusInternalServerError)
		return
	}
	requiresApproval, err := gate.RequiresApproval(r.Context(), h.repo, project,
		execution.EnvironmentID)
	if err != nil {
		http.Error(w, "Cannot check approval", http.StatusInternalServerError)
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
	retried, result, err := h.repo.CreateRunbookExecution(r.Context(),
		repository.RunbookExecutionRequest{
			ProjectID: execution.ProjectID, RunbookID: execution.RunbookID,
			VersionID:     execution.RunbookVersionID,
			EnvironmentID: execution.EnvironmentID,
			ActorUserID:   actor, Status: status,
		})
	if err != nil {
		http.Error(w, "Cannot retry execution", http.StatusInternalServerError)
		return
	}
	if status == "pending" {
		go h.runner.Run(context.Background(), result.Deployment.ID,
			result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%d/runbooks/executions/%d",
		execution.ProjectID, retried.ID), http.StatusSeeOther)
}
