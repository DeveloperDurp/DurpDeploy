package api

import (
	"net/http"

	"durpdeploy/internal/db"
)

// swagger:route GET /deployments/{id}/staging deployments listDeploymentStaging
//
// List active staging transfer metadata. File contents are omitted.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: StagingListResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	500: body:ServerError
func (h *DeploymentHandler) ListStaging(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, 400, "Invalid deployment ID")
		return
	}
	rows, err := h.repo.Queries.ListStagingSnapshots(r.Context(), id)
	if err != nil {
		RespondError(w, 500, "Could not list staging snapshots")
		return
	}
	if rows == nil {
		rows = []db.DeploymentStagingSnapshot{}
	}
	RespondJSON(w, 200, rows)
}

// swagger:parameters listDeploymentStaging
//
//nolint:unused // Used by Swagger specification generation.
type stagingPathParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:response StagingListResponse
//
//nolint:unused // Used by Swagger specification generation.
type stagingListResponse struct {
	// in: body
	Body []db.DeploymentStagingSnapshot
}
