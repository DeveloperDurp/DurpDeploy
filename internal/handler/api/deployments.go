package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/deploymentstate"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

type DeploymentHandler struct {
	repo       *repository.Repository
	runner     *runner.DeploymentRunner
	dispatcher *dispatch.Dispatcher
}

func NewDeploymentHandler(
	repo *repository.Repository,
	r *runner.DeploymentRunner,
	dispatchers ...*dispatch.Dispatcher,
) *DeploymentHandler {
	dispatcher := dispatch.New(repo, nil, r)
	if len(dispatchers) > 0 && dispatchers[0] != nil {
		dispatcher = dispatchers[0]
	}
	return &DeploymentHandler{repo: repo, runner: r, dispatcher: dispatcher}
}

type deploymentCreateRequest struct {
	ReleaseID     int64  `json:"release_id"`
	EnvironmentID int64  `json:"environment_id"`
	TargetMode    string `json:"target_mode"`
	AgentLabelID  int64  `json:"agent_label_id"`
	AgentStrategy string `json:"agent_strategy"`
	Force         bool   `json:"force"`
	Note          string `json:"note"`
}

type deploymentResponse struct {
	db.Deployment
	Dispatch deploymentstate.Dispatch `json:"dispatch"`
}

type deploymentStatusResponse struct {
	Status   string                   `json:"status"`
	Dispatch deploymentstate.Dispatch `json:"dispatch"`
}

func deploymentWithRouting(
	ctx context.Context,
	repo *repository.Repository,
	deployment db.Deployment,
) (deploymentResponse, error) {
	routing, err := deploymentstate.Load(ctx, repo, deployment.ID)
	if err != nil {
		return deploymentResponse{}, err
	}
	return deploymentResponse{Deployment: deployment, Dispatch: routing}, nil
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

	mode := req.TargetMode
	if mode == "" {
		mode = "default"
	}
	user := auth.UserFromContext(r.Context())
	deployment, err := dispatch.NewCreationService(h.repo, h.dispatcher).Create(
		r.Context(),
		dispatch.CreateRequest{
			ProjectID: projectID, ReleaseID: req.ReleaseID,
			EnvironmentID: req.EnvironmentID, Force: req.Force,
			Admin: user != nil && user.Role == "admin", Note: req.Note,
			Routing: dispatch.Input{
				Source: dispatch.SourceRequest, Mode: mode,
				LabelID: req.AgentLabelID, Strategy: req.AgentStrategy,
			},
		},
	)
	if err != nil {
		writeDeploymentCreationError(w, err)
		return
	}

	RespondJSON(w, http.StatusCreated, deployment)
}

func writeDeploymentCreationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dispatch.ErrReleaseNotFound),
		errors.Is(err, dispatch.ErrReleaseProjectMismatch):
		RespondError(w, http.StatusNotFound, "Release not found")
	case errors.Is(err, dispatch.ErrProjectNotFound):
		RespondError(w, http.StatusNotFound, "Project not found")
	case errors.Is(err, dispatch.ErrEnvironmentNotFound):
		RespondError(w, http.StatusNotFound, "Environment not found")
	case errors.Is(err, dispatch.ErrInvalidPolicy),
		errors.Is(err, dispatch.ErrLabelNotFound),
		errors.Is(err, dispatch.ErrNoEligibleAgents),
		errors.Is(err, dispatch.ErrPromotionBlocked):
		RespondError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, dispatch.ErrForceForbidden):
		RespondError(w, http.StatusForbidden, err.Error())
	default:
		RespondError(w, http.StatusInternalServerError, err.Error())
	}
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
	var fDispatchState sql.NullString
	if value := r.URL.Query().Get("remote_state"); value != "" {
		if !deploymentstate.ValidFilterState(value) {
			RespondError(w, http.StatusBadRequest, "invalid remote_state")
			return
		}
		fDispatchState = sql.NullString{String: value, Valid: true}
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
			FProjectID:     fProjectID,
			FEnvID:         fEnvID,
			FStatus:        fStatus,
			FDispatchState: fDispatchState,
			FFromUnix:      fFromUnix,
			FToUnix:        fToUnix,
			PageOffset:     offset,
			PageLimit:      limit,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountDeploymentsWithRefsFiltered(
		r.Context(),
		db.CountDeploymentsWithRefsFilteredParams{
			FProjectID:     fProjectID,
			FEnvID:         fEnvID,
			FStatus:        fStatus,
			FDispatchState: fDispatchState,
			FFromUnix:      fFromUnix,
			FToUnix:        fToUnix,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(deployments))
	for i, d := range deployments {
		deployment := db.Deployment{
			ID:            d.ID,
			ReleaseID:     d.ReleaseID,
			EnvironmentID: d.EnvironmentID,
			Status:        d.Status,
			StartedAt:     d.StartedAt,
			FinishedAt:    d.FinishedAt,
			CreatedAt:     d.CreatedAt,
			Forced:        d.Forced,
			Note:          d.Note,
		}
		response, err := deploymentWithRouting(r.Context(), h.repo, deployment)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items[i] = response
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
	response, err := deploymentWithRouting(r.Context(), h.repo, deployment)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, response)
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
	routing, err := deploymentstate.Load(r.Context(), h.repo, depID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, deploymentStatusResponse{
		Status: deployment.Status, Dispatch: routing,
	})
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

	err = dispatch.NewCreationService(h.repo, h.dispatcher).Approve(
		r.Context(), depID,
		dispatch.Approval{ApprovedBy: user.Name, ApproverUserID: user.ID},
	)
	if errors.Is(err, dispatch.ErrDeploymentNotPending) {
		RespondJSON(
			w, http.StatusConflict,
			map[string]string{"error": "deployment_not_pending"},
		)
		return
	}
	if errors.Is(err, dispatch.ErrDeploymentNotFound) {
		RespondError(w, http.StatusNotFound, "Deployment not found")
		return
	}
	if errors.Is(err, dispatch.ErrNoEligibleAgents) {
		RespondError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
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
	if deployment.ParentDeploymentID.Valid {
		RespondError(w, http.StatusConflict, "Child deployments cannot be redeployed")
		return
	}
	routing, err := deploymentstate.Load(r.Context(), h.repo, depID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deployment.Status != "succeeded" && deployment.Status != "failed" &&
		deployment.Status != "cancelled" && !routing.IsUncertainTerminal() {
		RespondError(
			w,
			http.StatusConflict,
			"Can only redeploy terminal deployments",
		)
		return
	}
	release, err := h.repo.Queries.GetRelease(r.Context(), deployment.ReleaseID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "Release not found")
		return
	}
	newDeployment, err := dispatch.NewCreationService(
		h.repo, h.dispatcher,
	).Create(r.Context(), dispatch.CreateRequest{
		ProjectID: release.ProjectID, ReleaseID: deployment.ReleaseID,
		EnvironmentID: deployment.EnvironmentID,
		Note:          fmt.Sprintf("Redeploy of #%d", deployment.ID),
		Routing: dispatch.Input{
			Source: dispatch.SourceRedeploy, Mode: "default",
		},
	})
	if err != nil {
		writeDeploymentCreationError(w, err)
		return
	}
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

	state, err := dispatch.NewCancellationService(h.repo, h.runner).Cancel(
		r.Context(),
		depID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		if errors.Is(err, dispatch.ErrCancellationState) {
			RespondError(
				w,
				http.StatusUnprocessableEntity,
				"Cannot cancel a deployment that is not running",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, map[string]string{"status": state})
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
//	  201: body:Deployment
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
	if deployment.ParentDeploymentID.Valid {
		RespondError(w, http.StatusConflict, "Child deployments cannot be retried")
		return
	}
	routing, err := deploymentstate.Load(r.Context(), h.repo, depID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if routing.IsUncertainTerminal() {
		RespondError(
			w,
			http.StatusConflict,
			"Use redeploy to create a new deployment after remote work is uncertain",
		)
		return
	}

	newDeployment, err := dispatch.NewCreationService(
		h.repo, h.dispatcher,
	).Retry(r.Context(), deployment)
	if errors.Is(err, dispatch.ErrNoEligibleAgents) {
		RespondError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, dispatch.ErrPromotionBlocked) {
		RespondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusCreated, newDeployment)
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
