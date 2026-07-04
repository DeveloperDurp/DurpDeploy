package handler

import (
	"fmt"
	"net/http"
	"strconv"

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
