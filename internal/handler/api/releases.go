package api

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type ReleaseHandler struct {
	repo *repository.Repository
}

func NewReleaseHandler(repo *repository.Repository) *ReleaseHandler {
	return &ReleaseHandler{repo: repo}
}

// releaseWithVariables is the JSON shape returned by GetRelease.
type releaseWithVariables struct {
	ID        int64                 `json:"id"`
	ProjectID int64                 `json:"project_id"`
	Version   string                `json:"version"`
	StepsJSON string                `json:"steps_json"`
	CreatedAt int64                 `json:"created_at"`
	Variables []releaseVariableJSON `json:"variables"`
}

type releaseVariableJSON struct {
	ID            int64          `json:"id"`
	ReleaseID     int64          `json:"release_id"`
	Name          string         `json:"name"`
	Value         sql.NullString `json:"value"`
	EnvironmentID sql.NullInt64  `json:"environment_id"`
	Secret        int64          `json:"secret"`
}

// swagger:route GET /projects/{id}/releases releases listReleases
//
// List releases for a project.
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
//	  200: body:ReleaseListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ReleaseHandler) ListReleases(w http.ResponseWriter, r *http.Request) {
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
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	releases, err := h.repo.Queries.ListReleasesByProjectPaginated(
		r.Context(),
		db.ListReleasesByProjectPaginatedParams{
			ProjectID: projectID,
			Limit:     limit,
			Offset:    offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountReleasesByProject(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(releases))
	for i, r := range releases {
		items[i] = r
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

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
		r.Context(),
		h.repo,
		projectID,
		body.Version,
	)
	if err != nil {
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

// swagger:route GET /projects/{id}/releases/{relId} releases getRelease
//
// Get a release with its variable snapshots.
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
//	  200: body:ReleaseWithVariablesResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ReleaseHandler) GetRelease(w http.ResponseWriter, r *http.Request) {
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

	variables, err := h.repo.ListReleaseVariablesByRelease(
		r.Context(),
		releaseID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	varsJSON := make([]releaseVariableJSON, len(variables))
	for i, v := range variables {
		varsJSON[i] = releaseVariableJSON{
			ID:            v.ID,
			ReleaseID:     v.ReleaseID,
			Name:          v.Name,
			Value:         v.Value,
			EnvironmentID: v.EnvironmentID,
			Secret:        v.Secret,
		}
	}

	RespondJSON(w, http.StatusOK, releaseWithVariables{
		ID:        release.ID,
		ProjectID: release.ProjectID,
		Version:   release.Version,
		StepsJSON: release.StepsJson,
		CreatedAt: release.CreatedAt,
		Variables: varsJSON,
	})
}
