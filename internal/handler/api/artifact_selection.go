package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/db"
)

// swagger:model ArtifactSelection
type artifactSelection struct {
	RepositoryID int64 `json:"repository_id"`
}

// swagger:route GET /projects/{id}/artifact-repository artifacts getArtifactRepository
// Read the repository selected for future release snapshots; zero disables artifacts.
// Responses:
// 200: body:ArtifactSelection
func (h *ArtifactHandler) Selection(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	row, err := h.repo.Queries.GetProjectArtifactRepository(
		r.Context(),
		projectID,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		artifactError(w, err)
		return
	}
	RespondJSON(w, 200, artifactSelection{row.ID})
}

// swagger:route PUT /projects/{id}/artifact-repository artifacts selectArtifactRepository
// Select one project repository for future snapshots, or zero to disable.
// Responses:
// 200: body:ArtifactSelection
func (h *ArtifactHandler) Select(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	var req artifactSelection
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.RepositoryID < 0 {
		RespondError(w, 422, "Invalid repository ID")
		return
	}
	if err := h.repo.SelectArtifactRepository(
		r.Context(),
		db.SelectProjectArtifactRepositoryParams{
			ProjectID:    projectID,
			RepositoryID: req.RepositoryID,
		},
	); err != nil {
		artifactError(w, err)
		return
	}
	RespondJSON(w, 200, req)
}

// swagger:route GET /projects/{id}/releases/{relId}/artifact artifacts getReleaseArtifact
// Read a release's pinned ZIP; null means no artifact.
// Responses:
// 200: body:ReleaseArtifactResponse
func (h *ArtifactHandler) ReleasePin(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "relId")
	if !ok {
		return
	}
	release, err := h.repo.Queries.GetRelease(r.Context(), id)
	if err == nil && release.ProjectID != projectID {
		err = sql.ErrNoRows
	}
	if err != nil {
		artifactError(w, err)
		return
	}
	pin, err := h.repo.Queries.GetReleaseArtifact(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		RespondJSON(w, 200, nil)
		return
	}
	if err != nil {
		artifactError(w, err)
		return
	}
	RespondJSON(w, 200, publicReleaseArtifact(pin))
}

// swagger:model ReleaseArtifactResponse
type releaseArtifactResponse struct {
	ReleaseID       int64  `json:"release_id"`
	RepositoryID    int64  `json:"repository_id"`
	URL             string `json:"url"`
	Version         string `json:"version"`
	SHA256          string `json:"sha256"`
	Size            int64  `json:"size"`
	SourceReleaseID *int64 `json:"source_release_id"`
}

func publicReleaseArtifact(row db.ReleaseArtifact) releaseArtifactResponse {
	var sourceReleaseID *int64
	if row.SourceReleaseID.Valid {
		sourceReleaseID = &row.SourceReleaseID.Int64
	}
	return releaseArtifactResponse{
		row.ReleaseID,
		row.RepositoryID,
		row.Url,
		row.Version,
		row.Sha256,
		row.Size,
		sourceReleaseID,
	}
}
