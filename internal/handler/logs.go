package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/httpstream"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"github.com/go-chi/chi/v5"
)

type LogHandler struct {
	broker *runner.LogBroker
	repo   *repository.Repository
}

func NewLogHandler(
	broker *runner.LogBroker,
	repo *repository.Repository,
) *LogHandler {
	return &LogHandler{broker: broker, repo: repo}
}

func (h *LogHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	deploymentID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}
	h.streamDeploymentLogs(w, r, deploymentID)
}

func (h *LogHandler) streamDeploymentLogs(
	w http.ResponseWriter,
	r *http.Request,
	deploymentID int64,
) {
	if r.URL.Query().Get("format") == "structured" {
		httpstream.StreamStepLogs(w, r, h.repo, h.broker, deploymentID)
		return
	}
	httpstream.StreamTextLogs(w, r, h.repo, h.broker, deploymentID, false)
}

func (h *LogHandler) ExportLogs(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	deploymentID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

	h.exportDeploymentLogs(w, r, deploymentID)
}

func (h *LogHandler) exportDeploymentLogs(
	w http.ResponseWriter,
	r *http.Request,
	deploymentID int64,
) {
	deployment, err := h.repo.Queries.GetDeployment(r.Context(), deploymentID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), deployment.ReleaseID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(`attachment; filename="deployment-%d.log"`, deploymentID),
	)
	w.WriteHeader(http.StatusOK)

	if _, err := fmt.Fprintf(
		w,
		"=== deployment #%d | project=%s | release=%s | env=%s | status=%s ===\n",
		deploymentID,
		project.Name,
		release.Version,
		environment.Name,
		deployment.Status,
	); err != nil {
		return
	}
	err = h.repo.ForEachDeploymentLogByDeploymentAsc(
		r.Context(),
		deploymentID,
		func(lg db.DeploymentLog) error {
			ts := time.Unix(lg.CreatedAt, 0).UTC().Format("2006-01-02 15:04:05")
			if lg.StepName.Valid {
				_, err := fmt.Fprintf(
					w,
					"[%s] [%s] %s\n",
					ts,
					lg.StepName.String,
					lg.Line,
				)
				return err
			}
			_, err := fmt.Fprintf(w, "[%s] %s\n", ts, lg.Line)
			return err
		},
	)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(w)

}
