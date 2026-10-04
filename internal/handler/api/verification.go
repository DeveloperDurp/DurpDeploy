package api

import (
	"database/sql"
	"errors"
	"net/http"
)

// swagger:route GET /deployments/{id}/verification deployments getDeploymentVerification
//
// Get the snapshotted verification check and its outcome.
//
//	Security:
//	  bearer:
//
// Responses:
//
//	200: body:DeploymentVerification
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	500: body:ServerError
func (h *DeploymentHandler) GetVerification(
	w http.ResponseWriter, r *http.Request,
) {
	id, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}
	if _, err := h.repo.Queries.GetDeployment(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Deployment not found")
		} else {
			RespondError(
				w,
				http.StatusInternalServerError,
				"Cannot load deployment",
			)
		}
		return
	}
	check, err := h.repo.Queries.GetDeploymentVerification(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		RespondJSON(w, http.StatusOK,
			map[string]string{"status": "disabled"})
		return
	}
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot load verification",
		)
		return
	}
	// Commands and URLs can contain inline credentials. Configuration lives
	// on environments; deployment inspection exposes only safe metadata.
	RespondJSON(w, http.StatusOK, verificationResponse{
		Type: check.Type, TimeoutSeconds: check.TimeoutSeconds,
		Status: check.Status, StartedAt: check.StartedAt,
		FinishedAt: check.FinishedAt,
	})
}

// swagger:model DeploymentVerification
type verificationResponse struct {
	Type           string        `json:"type,omitempty"`
	TimeoutSeconds int64         `json:"timeout_seconds,omitempty"`
	Status         string        `json:"status"`
	StartedAt      sql.NullInt64 `json:"started_at"`
	FinishedAt     sql.NullInt64 `json:"finished_at"`
}
