package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Replay historical logs first
	logs, err := h.repo.Queries.ListDeploymentLogsByDeployment(
		r.Context(),
		deploymentID,
	)
	if err == nil {
		for _, log := range logs {
			fmt.Fprintf(w, "data: %s\n\n", log.Line)
			flusher.Flush()
		}
	}

	ch := h.broker.Subscribe(deploymentID)
	defer h.broker.Unsubscribe(deploymentID, ch)

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

	environment, err := h.repo.Queries.GetEnvironment(r.Context(), deployment.EnvironmentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logs, err := h.repo.Queries.ListDeploymentLogsByDeployment(r.Context(), deploymentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	header := fmt.Sprintf(
		"=== deployment #%d | project=%s | release=%s | env=%s | status=%s ===\n",
		deploymentID, project.Name, release.Version, environment.Name, deployment.Status,
	)

	var buf strings.Builder
	buf.WriteString(header)

	for i := len(logs) - 1; i >= 0; i-- {
		lg := logs[i]
		ts := time.Unix(lg.CreatedAt, 0).UTC().Format("2006-01-02 15:04:05")
		if lg.StepName.Valid {
			buf.WriteString(fmt.Sprintf("[%s] [%s] %s\n", ts, lg.StepName.String, lg.Line))
		} else {
			buf.WriteString(fmt.Sprintf("[%s] %s\n", ts, lg.Line))
		}
	}
	buf.WriteByte('\n')

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="deployment-%d.log"`, deploymentID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(buf.String())) // ponytail: builds full body in memory; chunked for 100k+ lines if needed later
}
