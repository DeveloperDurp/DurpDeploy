package api

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
)

// swagger:route POST /projects/{id}/releases/{relId}/refresh releases refreshRelease
//
// Refresh a release snapshot from current project steps and variables.
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
//	  200: body:Release
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ReleaseHandler) RefreshRelease(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}
	releaseID, err := strconv.ParseInt(chi.URLParam(r, "relId"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid release ID")
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Release not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if release.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Release not found")
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stepsJSON, err := handler.BuildReleaseStepsJSON(steps)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	qtx := h.repo.Queries.WithTx(tx)

	if _, err := qtx.UpdateRelease(r.Context(), db.UpdateReleaseParams{
		ID:        releaseID,
		ProjectID: projectID,
		Version:   release.Version,
		StepsJson: stepsJSON,
	}); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := qtx.DeleteReleaseVariablesByRelease(
		r.Context(),
		releaseID,
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	variables, err := h.repo.ListVariablesByProject(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	for _, v := range variables {
		encValue, err := h.repo.EncryptValue(v.Value)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := qtx.CreateReleaseVariable(
			r.Context(),
			db.CreateReleaseVariableParams{
				ReleaseID:     releaseID,
				Name:          v.Name,
				Value:         encValue,
				EnvironmentID: v.EnvironmentID,
				Secret:        v.Secret,
			},
		); err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, updated)
}
