package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
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

// ExportLogs returns deployment logs as a plain text file.
// swagger:route GET /deployments/{id}/logs.txt logs exportLogs
//
// Export deployment logs as text.
//
//	Produces:
//	- text/plain
//
//	Schemes: http, https
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

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), depID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), deployment.ReleaseID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), release.ProjectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	environment, err := h.repo.Queries.GetEnvironment(
		r.Context(),
		deployment.EnvironmentID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(`attachment; filename="deployment-%d.log"`, depID),
	)
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(
		w,
		"=== deployment #%d | project=%s | release=%s | env=%s | status=%s ===\n",
		depID,
		project.Name,
		release.Version,
		environment.Name,
		deployment.Status,
	)
	err = h.repo.ForEachDeploymentLogByDeploymentAsc(
		r.Context(),
		depID,
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

// GetLog returns a single deployment log entry.
// swagger:route GET /deployments/{id}/logs/{logId} logs getLog
//
// Get a single deployment log entry.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
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
type swaggerTextResponse struct {
	Body string
}
