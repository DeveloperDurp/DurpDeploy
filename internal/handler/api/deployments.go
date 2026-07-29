package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

type DeploymentHandler struct {
	repo   *repository.Repository
	runner *runner.DeploymentRunner
}

func NewDeploymentHandler(
	repo *repository.Repository,
	r *runner.DeploymentRunner,
) *DeploymentHandler {
	return &DeploymentHandler{repo: repo, runner: r}
}

type deploymentCreateRequest struct {
	ReleaseID     int64 `json:"release_id"`
	EnvironmentID int64 `json:"environment_id"`
}

func deploymentIDFromRequest(r *http.Request) (int64, error) {
	if v := chi.URLParam(r, "depId"); v != "" {
		return strconv.ParseInt(v, 10, 64)
	}
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}

// swagger:route POST /projects/{id}/deployments deployments createDeployment
//
// Start a deployment.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  201: body:Deployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *DeploymentHandler) CreateDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req deploymentCreateRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.ReleaseID == 0 || req.EnvironmentID == 0 {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"release_id and environment_id are required",
		)
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), req.ReleaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Release not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if release.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Release not found")
		return
	}

	if _, err := h.repo.Queries.GetEnvironment(
		r.Context(),
		req.EnvironmentID,
	); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Environment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	deployment, err := h.repo.Queries.CreateDeployment(
		r.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     req.ReleaseID,
			EnvironmentID: req.EnvironmentID,
			Status:        "pending",
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	go h.runner.Run(
		context.Background(),
		deployment.ID,
		release.ID,
		req.EnvironmentID,
	)

	RespondJSON(w, http.StatusCreated, deployment)
}

// swagger:route GET /deployments deployments listAllDeployments
//
// List all deployments across all projects.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:DeploymentListResponse
//	  401: body:UnauthorizedError
//	  500: body:ServerError

// swagger:route GET /projects/{id}/deployments deployments listDeployments
//
// List deployments for a project.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:DeploymentListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *DeploymentHandler) ListDeployments(
	w http.ResponseWriter,
	r *http.Request,
) {
	var fProjectID sql.NullInt64
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		projectID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "Invalid project ID")
			return
		}
		if _, err := h.repo.Queries.GetProject(
			r.Context(),
			projectID,
		); err != nil {
			if err == sql.ErrNoRows {
				RespondError(w, http.StatusNotFound, "Project not found")
				return
			}
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		fProjectID = sql.NullInt64{Int64: projectID, Valid: true}
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	var fEnvID sql.NullInt64
	if v := r.URL.Query().Get("env_id"); v != "" {
		envID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || envID <= 0 {
			RespondError(
				w,
				http.StatusBadRequest,
				"env_id must be a positive integer",
			)
			return
		}
		fEnvID = sql.NullInt64{Int64: envID, Valid: true}
	}

	var fStatus sql.NullString
	if v := r.URL.Query().Get("status"); v != "" {
		fStatus = sql.NullString{String: v, Valid: true}
	}

	var fFromUnix, fToUnix sql.NullInt64
	if v := r.URL.Query().Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			RespondError(
				w,
				http.StatusBadRequest,
				"from must be a unix timestamp",
			)
			return
		}
		fFromUnix = sql.NullInt64{Int64: n, Valid: true}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			RespondError(
				w,
				http.StatusBadRequest,
				"to must be a unix timestamp",
			)
			return
		}
		fToUnix = sql.NullInt64{Int64: n, Valid: true}
	}

	deployments, err := h.repo.Queries.ListDeploymentsWithRefsFiltered(
		r.Context(),
		db.ListDeploymentsWithRefsFilteredParams{
			FProjectID: fProjectID,
			FEnvID:     fEnvID,
			FStatus:    fStatus,
			FFromUnix:  fFromUnix,
			FToUnix:    fToUnix,
			PageOffset: offset,
			PageLimit:  limit,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountDeploymentsWithRefsFiltered(
		r.Context(),
		db.CountDeploymentsWithRefsFilteredParams{
			FProjectID: fProjectID,
			FEnvID:     fEnvID,
			FStatus:    fStatus,
			FFromUnix:  fFromUnix,
			FToUnix:    fToUnix,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(deployments))
	for i, d := range deployments {
		items[i] = d
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route GET /deployments/{id} deployments getDeployment
//
// Get a deployment.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:Deployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *DeploymentHandler) GetDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, deployment)
}

// GetDeploymentStatus returns the current deployment status.
// swagger:route GET /deployments/{id}/status deployments getDeploymentStatus
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:DeploymentStatusResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *DeploymentHandler) GetDeploymentStatus(
	w http.ResponseWriter,
	r *http.Request,
) {
	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(
		w,
		http.StatusOK,
		map[string]string{"status": deployment.Status},
	)
}

// ApproveDeployment approves a deployment pending approval.
// swagger:route POST /deployments/{id}/approve deployments approveDeployment
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:Deployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *DeploymentHandler) ApproveDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Role != "admin" {
		RespondError(w, http.StatusForbidden, "Admin access required")
		return
	}

	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deployment.Status != "pending_approval" {
		RespondError(
			w,
			http.StatusBadRequest,
			"Deployment is not pending approval",
		)
		return
	}

	if err := h.repo.Queries.UpdateDeploymentStatus(
		r.Context(),
		db.UpdateDeploymentStatusParams{
			ID:     depID,
			Status: "approved",
		},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// RedeployDeployment creates a new deployment from a terminal one.
// swagger:route POST /deployments/{id}/redeploy deployments redeployDeployment
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  201: body:Deployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *DeploymentHandler) RedeployDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deployment.Status != "succeeded" && deployment.Status != "failed" &&
		deployment.Status != "cancelled" {
		RespondError(
			w,
			http.StatusConflict,
			"Can only redeploy terminal deployments",
		)
		return
	}

	newDeployment, err := h.repo.Queries.CreateDeployment(
		r.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     deployment.ReleaseID,
			EnvironmentID: deployment.EnvironmentID,
			Status:        "pending",
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go h.runner.Run(
		r.Context(),
		newDeployment.ID,
		newDeployment.ReleaseID,
		newDeployment.EnvironmentID,
	)
	RespondJSON(w, http.StatusCreated, newDeployment)
}

// swagger:route POST /deployments/{id}/cancel deployments cancelDeployment
//
// Cancel a running deployment.
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:Deployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *DeploymentHandler) CancelDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deployment.Status != "running" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"Cannot cancel a deployment that is not running",
		)
		return
	}

	if err := h.runner.Cancel(depID); err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// swagger:route POST /deployments/{id}/retry deployments retryDeployment
//
// Retry a failed or cancelled deployment.
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:Deployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *DeploymentHandler) RetryDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deployment.Status != "failed" && deployment.Status != "cancelled" {
		RespondError(
			w,
			http.StatusBadRequest,
			"Can only retry failed or cancelled deployments",
		)
		return
	}

	if err := h.repo.Queries.UpdateDeploymentStatus(
		r.Context(),
		db.UpdateDeploymentStatusParams{
			ID:     depID,
			Status: "pending",
		},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	go h.runner.Run(
		r.Context(),
		depID,
		deployment.ReleaseID,
		deployment.EnvironmentID,
	)

	updated, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, updated)
}

// swagger:route GET /deployments/{id}/logs deployments listDeploymentLogs
//
// List deployment step logs.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:DeploymentLogListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *DeploymentHandler) ListDeploymentLogs(
	w http.ResponseWriter,
	r *http.Request,
) {
	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	logs, err := h.repo.Queries.ListDeploymentLogsByDeployment(
		r.Context(),
		depID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, logs)
}

// swagger:route GET /deployments/{id}/events deployments deploymentEvents
//
// Stream deployment events as Server-Sent Events.
//
//	Produces:
//	- text/event-stream
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:StreamResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
func (h *DeploymentHandler) DeploymentEvents(
	w http.ResponseWriter,
	r *http.Request,
) {
	depID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	if _, err := h.repo.Queries.GetDeployment(r.Context(), depID); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		RespondError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	logs, err := h.repo.Queries.ListDeploymentLogsByDeployment(
		r.Context(),
		depID,
	)
	if err == nil {
		for _, log := range logs {
			fmt.Fprintf(w, "data: %s\n\n", log.Line)
			flusher.Flush()
		}
	}

	ch := h.runner.Broker().Subscribe(depID)
	defer h.runner.Broker().Unsubscribe(depID, ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}
