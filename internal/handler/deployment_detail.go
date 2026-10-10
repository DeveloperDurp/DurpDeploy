package handler

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

func (h *DeploymentHandler) GetDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	deployment, ok := h.deploymentFromRequest(w, r)
	if !ok {
		return
	}
	id := deployment.ID

	release, err := h.repo.Queries.GetDeploymentRelease(
		r.Context(),
		deployment.ReleaseID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stepSource, err := h.repo.Queries.GetDeploymentStepSource(r.Context(), id)
	if err != nil && err != sql.ErrNoRows {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err == nil {
		release.StepsJson = stepSource.StepsJson
	}

	project, err := h.repo.Queries.GetProject(r.Context(), release.ProjectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environment, err := h.repo.Queries.GetEnvironment(
		r.Context(),
		deployment.EnvironmentID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logs, err := h.repo.Queries.ListDeploymentLogsByDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	queueCtx, waiting, ok := h.deploymentQueueState(w, r, &deployment)
	if !ok {
		return
	}
	queueCtx = pages.WithArtifactDecisionWarning(
		queueCtx,
		r.URL.Query().Get("artifact_decision"),
	)
	if isFragmentRequest(r) {
		if err := pages.DeploymentDetail(project, release, environment, deployment, logs, waiting != 0).
			Render(queueCtx, w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.DeploymentDetailPage(project, release, environment, deployment, logs, waiting != 0, r.URL.Path).
			Render(queueCtx, w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (h *DeploymentHandler) GetDeploymentStatus(
	w http.ResponseWriter,
	r *http.Request,
) {
	deployment, ok := h.deploymentFromRequest(w, r)
	if !ok {
		return
	}

	queueCtx, waiting, ok := h.deploymentQueueState(w, r, &deployment)
	if !ok {
		return
	}
	if err := pages.DeploymentStatusUpdate(deployment, waiting != 0).
		Render(queueCtx, w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *DeploymentHandler) deploymentFromRequest(
	w http.ResponseWriter,
	r *http.Request,
) (db.Deployment, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return db.Deployment{}, false
	}
	deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return db.Deployment{}, false
	}
	return deployment, true
}

func (h *DeploymentHandler) deploymentQueueState(
	w http.ResponseWriter,
	r *http.Request,
	deployment *db.Deployment,
) (context.Context, int64, bool) {
	waiting, err := h.repo.Queries.DeploymentWaitingForAgents(
		r.Context(),
		deployment.ID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, 0, false
	}
	queueCtx, err := pages.DeploymentQueueContext(
		r.Context(),
		h.repo,
		deployment.ID,
		&deployment.Status,
	)
	if err != nil {
		http.Error(
			w,
			"Cannot read deployment queue",
			http.StatusInternalServerError,
		)
		return nil, 0, false
	}
	return queueCtx, waiting, true
}
