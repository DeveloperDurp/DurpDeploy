package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/go-chi/chi/v5"
)

// StreamLogs streams deployment logs as SSE or NDJSON.
// swagger:route GET /deployments/{id}/logs/stream logs streamLogs
//
// Stream deployment logs.
//
//	Produces:
//	- text/event-stream
//	- application/x-ndjson
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
func (h *LogHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
		return
	}
	format := r.URL.Query().Get("format")
	ndjson := format == "ndjson"
	if format != "" && format != "sse" && format != "ndjson" {
		RespondError(w, http.StatusBadRequest, "format must be sse or ndjson")
		return
	}
	if _, err := h.repo.Queries.GetDeployment(
		r.Context(),
		deploymentID,
	); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cursor, err := lastEventID(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid Last-Event-ID")
		return
	}
	if ndjson {
		w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		RespondError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	ch := h.broker.Subscribe(deploymentID)
	defer h.broker.Unsubscribe(deploymentID, ch)
	for {
		err := h.repo.ForEachDeploymentLogAfterID(
			r.Context(),
			repository.DeploymentLogCursor{
				DeploymentID: deploymentID,
				AfterID:      cursor,
			},
			func(log db.DeploymentLog) error {
				writeLogLine(w, flusher, log, ndjson)
				if log.ID > cursor {
					cursor = log.ID
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

func lastEventID(r *http.Request) (int64, error) {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func writeLogLine(
	w http.ResponseWriter,
	flusher http.Flusher,
	log db.DeploymentLog,
	ndjson bool,
) {
	if ndjson {
		step := ""
		if log.StepName.Valid {
			step = log.StepName.String
		}
		if err := json.NewEncoder(w).Encode(struct {
			ID   int64  `json:"id"`
			Line string `json:"line"`
			Step string `json:"step"`
		}{ID: log.ID, Line: log.Line, Step: step}); err != nil {
			return
		}
	} else {
		_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", log.ID, log.Line)
	}
	flusher.Flush()
}
