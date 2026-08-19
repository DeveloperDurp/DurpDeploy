package api

import (
	"database/sql"
	"fmt"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// DeploymentEvents streams deployment logs as SSE.
// swagger:route GET /deployments/{id}/events deployments deploymentEvents
//
// Stream deployment events as Server-Sent Events.
//
//	Produces:
//	- text/event-stream
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
func (h *DeploymentHandler) DeploymentEvents(
	w http.ResponseWriter,
	r *http.Request,
) {
	deploymentID, err := deploymentIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid deployment ID")
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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		RespondError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	ch := h.runner.Broker().Subscribe(deploymentID)
	defer h.runner.Broker().Unsubscribe(deploymentID, ch)
	for {
		err := h.repo.ForEachDeploymentLogAfterID(
			r.Context(),
			repository.DeploymentLogCursor{
				DeploymentID: deploymentID,
				AfterID:      cursor,
			},
			func(log db.DeploymentLog) error {
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", log.ID, log.Line)
				flusher.Flush()
				cursor = log.ID
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
