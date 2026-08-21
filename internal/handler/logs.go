package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"durpdeploy/internal/db"
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
	cursor, err := streamLastEventID(r)
	if err != nil {
		http.Error(w, "Invalid Last-Event-ID", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := h.broker.Subscribe(deploymentID)
	defer h.broker.Unsubscribe(deploymentID, ch)
	for {
		err := h.repo.ForEachDeploymentLogAfterID(
			r.Context(),
			repository.DeploymentLogCursor{
				DeploymentID:  deploymentID,
				AfterSequence: cursor,
			},
			func(log db.DeploymentLog) error {
				_, _ = fmt.Fprintf(
					w,
					"id: %d\ndata: %s\n\n",
					log.Sequence,
					log.Line,
				)
				flusher.Flush()
				if log.Sequence > cursor {
					cursor = log.Sequence
				}
				return nil
			},
		)
		if err != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		}
	}
}

func streamLastEventID(r *http.Request) (int64, error) {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func (h *LogHandler) ExportLogs(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	deploymentID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

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

	fmt.Fprintf(
		w,
		"=== deployment #%d | project=%s | release=%s | env=%s | status=%s ===\n",
		deploymentID,
		project.Name,
		release.Version,
		environment.Name,
		deployment.Status,
	)
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
