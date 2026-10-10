package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

func (h *DeploymentHandler) GetDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

	waiting, err := h.repo.Queries.DeploymentWaitingForAgents(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	queueCtx, err := pages.DeploymentQueueContext(
		r.Context(),
		h.repo,
		id,
		&deployment.Status,
	)
	if err != nil {
		http.Error(
			w,
			"Cannot read deployment queue",
			http.StatusInternalServerError,
		)
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
