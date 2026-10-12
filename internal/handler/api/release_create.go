package api

import (
	"database/sql"
	"durpdeploy/internal/handler"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
)

// swagger:route POST /projects/{id}/releases releases createRelease
//
// Create a release snapshot for a project.
//
//	Consumes:
//	- application/json
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
//	  413: body:RequestEntityTooLargeError
//	  201: body:Release
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *ReleaseHandler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	if _, err := h.repo.Queries.GetProject(r.Context(), projectID); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Project not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var body struct {
		Version string `json:"version"`
	}
	if !readJSONBool(w, r, &body) {
		return
	}
	if body.Version == "" {
		RespondError(w, http.StatusUnprocessableEntity, "version is required")
		return
	}

	release, err := handler.CreateReleaseSnapshot(
		r.Context(), h.repo, projectID, body.Version,
	)
	if err != nil {
		if writeArtifactError(w, err) {
			return
		}
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A release with this version already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, release)
}
