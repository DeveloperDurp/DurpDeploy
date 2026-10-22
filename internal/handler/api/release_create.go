package api

import (
	"database/sql"
	"durpdeploy/internal/audit"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/provenance"
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
		provenance.Source
	}
	if !readJSONBool(w, r, &body) {
		return
	}
	if body.Version == "" {
		RespondError(w, http.StatusUnprocessableEntity, "version is required")
		return
	}

	if err := body.Validate(); err != nil {
		RespondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	request, ok := creationIdempotency(w, r, projectID, "release", body)
	if !ok || replayCreation(w, r, h.repo, request) {
		return
	}
	release, err := handler.CreateReleaseWithProvenance(
		r.Context(), h.repo, projectID, body.Version, body.Source, request,
	)
	if err != nil {
		if replayCreation(w, r, h.repo, request) {
			return
		}
		if writeArtifactError(w, err) {
			return
		}
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A release with this version already exists. "+
					"Enter a different version to create a new release.",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit.SetReleaseProvenance(r, release)
	RespondJSON(w, http.StatusCreated, release)
}
