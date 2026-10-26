package handler

import (
	"net/http"
	"strconv"

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
	httpstream.ExportTextLogs(w, r, h.repo, deploymentID,
		func(status int, message string) {
			http.Error(w, message, status)
		})
}
