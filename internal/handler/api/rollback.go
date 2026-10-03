package api

import (
	"net/http"

	"durpdeploy/internal/handler"
)

// swagger:route GET /deployments/{id}/rollback deployments previewRollback
//
// Preview the latest prior successful different release in the same project and environment.
//
//	Security:
//	  bearer:
//
// Responses:
//
//	200: body:RollbackPreview
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	500: body:ServerError
func (h *DeploymentHandler) PreviewRollback(
	w http.ResponseWriter, r *http.Request,
) {
	id, err := deploymentIDFromRequest(r)
	if err != nil || id <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}
	preview, err := h.repo.PreviewRollback(r.Context(), id)
	if err != nil {
		RespondError(w, handler.RollbackErrorStatus(err), err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, preview)
}

type rollbackRequest struct {
	TargetDeploymentID int64 `json:"target_deployment_id"`
}

// swagger:route POST /deployments/{id}/rollback deployments rollbackDeployment
//
// Create an audited deployment of the confirmed rollback target. Normal gates and approvals apply.
//
//	Security:
//	  bearer:
//
// Responses:
//
//	201: body:Deployment
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	413: body:RequestEntityTooLargeError
//	422: body:ValidationError
//	500: body:ServerError
func (h *DeploymentHandler) RollbackDeployment(
	w http.ResponseWriter, r *http.Request,
) {
	id, err := deploymentIDFromRequest(r)
	if err != nil || id <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}
	var req rollbackRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.TargetDeploymentID <= 0 {
		RespondError(
			w,
			http.StatusBadRequest,
			"Target deployment ID is required",
		)
		return
	}
	result, err := h.repo.CreateRollback(
		r.Context(),
		id,
		req.TargetDeploymentID,
	)
	if err != nil {
		if writeArtifactError(w, err) {
			return
		}
		RespondError(w, handler.RollbackErrorStatus(err), err.Error())
		return
	}
	h.startLocalDeployment(result)
	RespondJSON(w, http.StatusCreated, result.Deployment)
}
