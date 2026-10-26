package api

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/httpstream"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

type LogHandler struct {
	repo   *repository.Repository
	broker *runner.LogBroker
}

func NewLogHandler(
	broker *runner.LogBroker,
	repo *repository.Repository,
) *LogHandler {
	return &LogHandler{repo: repo, broker: broker}
}

// StreamLogs streams deployment logs as SSE or NDJSON.
// swagger:route GET /deployments/{id}/logs/stream logs streamLogs
//
// Stream deployment logs. The format=structured SSE option emits indexed
// step logs and lifecycle states, supports after and Last-Event-ID cursors,
// and ends with a complete event. Default SSE and NDJSON remain text streams.
// Add preview=true for marked previews using the configured line byte budget.
// The default preview budget is 16 KiB per text field.
//
//	Produces:
//	- text/event-stream
//	- application/x-ndjson
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:StreamResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
func (h *LogHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	depID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}
	h.streamDeploymentLogs(w, r, depID)
}

func (h *LogHandler) streamDeploymentLogs(
	w http.ResponseWriter,
	r *http.Request,
	depID int64,
) {
	format := r.URL.Query().Get("format")
	if format == "structured" {
		if _, err := httpstream.LogCursor(r); err != nil {
			RespondError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := httpstream.LogPreviewRequested(r); err != nil {
			RespondError(w, http.StatusBadRequest, err.Error())
			return
		}
		httpstream.StreamStepLogs(w, r, h.repo, h.broker, depID)
		return
	}
	ndjson := format == "ndjson"
	if format != "" && format != "sse" && format != "ndjson" {
		RespondError(
			w,
			http.StatusBadRequest,
			"format must be sse, ndjson or structured",
		)
		return
	}

	httpstream.StreamTextLogs(w, r, h.repo, h.broker, depID, ndjson)
}

// ExportLogs returns deployment logs as a plain text file.
// swagger:route GET /deployments/{id}/logs.txt logs exportLogs
//
// Export deployment logs as text.
//
//	Produces:
//	- text/plain
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:TextResponse
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *LogHandler) ExportLogs(w http.ResponseWriter, r *http.Request) {
	depID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}

	h.exportDeploymentLogs(w, r, depID)
}

func (h *LogHandler) exportDeploymentLogs(
	w http.ResponseWriter,
	r *http.Request,
	depID int64,
) {
	httpstream.ExportTextLogs(w, r, h.repo, depID,
		func(status int, message string) {
			RespondError(w, status, message)
		})
}

// GetLog returns a single deployment log entry.
// swagger:route GET /deployments/{id}/logs/{logId} logs getLog
//
// Get a single deployment log entry.
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:DeploymentLog
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *LogHandler) GetLog(w http.ResponseWriter, r *http.Request) {
	depID, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}
	logID, err := parseParamInt(r, "logId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid log ID")
		return
	}

	entry, err := h.repo.Queries.GetDeploymentLog(r.Context(), logID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Log entry not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// R2: refuse to serve a log that belongs to a different
	// deployment than the URL {id}. Deployment membership is already
	// validated by RequireDeploymentProjectAccess on the sub-group,
	// so this is the per-row check that closes the IDOR — without
	// it, a project-A member could pull project-B deployment logs
	// by guessing log IDs.
	if entry.DeploymentID != depID {
		RespondError(w, http.StatusNotFound, "Log entry not found")
		return
	}

	RespondJSON(w, http.StatusOK, entry)
}

// TextResponse is a plain text response.
// swagger:model TextResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerTextResponse struct {
	Body string
}
