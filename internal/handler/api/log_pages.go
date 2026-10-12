package api

import (
	"net/http"

	"durpdeploy/internal/handler"
)

// swagger:route GET /deployments/{id}/logs/page logs pageDeploymentLogs
// Read up to 500 log rows, ordered by increasing ID. Use next_before for older rows.
//
// Responses:
// 200: body:LogPage
// 400: body:BadRequestError
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 500: body:ServerError
func (h *LogHandler) Page(w http.ResponseWriter, r *http.Request) {
	id, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, 400, "Invalid deployment ID")
		return
	}
	h.page(w, r, id)
}

func (h *LogHandler) page(
	w http.ResponseWriter,
	r *http.Request,
	deploymentID int64,
) {
	before, err := handler.LogPageCursor(r)
	if err != nil {
		RespondError(w, 400, err.Error())
		return
	}
	page, err := h.repo.ReadLogPage(r.Context(), deploymentID, before)
	if err != nil {
		RespondError(w, 500, "Cannot read log page")
		return
	}
	RespondJSON(w, 200, page)
}

// swagger:route GET /projects/{id}/runbook-executions/{executionId}/logs/page runbooks pageRunbookLogs
// Read a bounded runbook log page. Use next_before for older rows.
//
// Responses:
// 200: body:LogPage
// 400: body:BadRequestError
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 500: body:ServerError
func (h *RunbookHandler) LogPage(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if ok {
		NewLogHandler(h.runner.Broker(), h.repo).page(
			w, r, execution.DeploymentID,
		)
	}
}

// swagger:route GET /projects/{id}/runbook-executions/{executionId}/logs.txt runbooks exportRunbookLogs
// Export all persisted runbook logs as text.
//
// Produces:
// - text/plain
//
// Responses:
// 200: body:TextResponse
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 500: body:ServerError
func (h *RunbookHandler) ExportLogs(w http.ResponseWriter, r *http.Request) {
	execution, ok := h.execution(w, r)
	if ok {
		NewLogHandler(h.runner.Broker(), h.repo).exportDeploymentLogs(
			w, r, execution.DeploymentID,
		)
	}
}
