package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/requestmeta"
	"github.com/go-chi/chi/v5"
)

// QueueMaintenanceResponse reports current queue owners and persisted blockers.
// swagger:model QueueMaintenanceResponse
type QueueMaintenanceResponse struct {
	Status string                     `json:"status"`
	Slots  []repository.QueueRecovery `json:"slots"`
}

// QueueReconciliationResponse reports whether this request released the slot.
// swagger:model QueueReconciliationResponse
type QueueReconciliationResponse struct {
	DeploymentID int64 `json:"deployment_id"`
	Changed      bool  `json:"changed"`
}

// swagger:parameters reconcileDeploymentQueue
//
//nolint:unused // Used by Swagger specification generation.
type reconcileDeploymentQueueParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:route POST /admin/maintenance/deployments/{id}/reconcile admin reconcileDeploymentQueue
//
// Reconcile one queue slot using existing cleanup evidence.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:QueueReconciliationResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	413: body:RequestEntityTooLargeError
//	500: body:ServerError
func (h *AdminHandler) ReconcileDeploymentQueue(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Role != "admin" {
		RespondError(w, http.StatusForbidden, "Administrator required")
		return
	}
	details := map[string]any{
		"ip":     requestmeta.ClientIP(r),
		"method": r.Method,
		"path":   r.URL.Path,
	}
	if token := auth.TokenFromContext(r.Context()); token != nil {
		details["api_token_id"] = token.ID
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not encode audit details",
		)
		return
	}
	changed, err := h.repo.ReconcileDeploymentQueue(
		r.Context(),
		id,
		db.CreateAuditLogParams{
			UserID:  sql.NullInt64{Int64: user.ID, Valid: true},
			Details: sql.NullString{String: string(encoded), Valid: true},
		},
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		RespondError(w, http.StatusNotFound, "Deployment not found")
	case errors.Is(err, repository.ErrQueueRecoveryBlocked):
		RespondError(w, http.StatusConflict, err.Error())
	case err != nil:
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not reconcile deployment queue",
		)
	default:
		// The required actor event commits with the queue mutation.
		audit.Suppress(r)
		RespondJSON(
			w,
			http.StatusOK,
			QueueReconciliationResponse{DeploymentID: id, Changed: changed},
		)
	}
}
