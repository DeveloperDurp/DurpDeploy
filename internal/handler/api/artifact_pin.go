package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/db"
)

// swagger:route GET /projects/{id}/releases/{relId}/artifact artifacts getReleaseArtifact
// Read a release's pinned ZIP; null means no artifact.
// Responses:
// 200: body:ReleaseArtifactResponse
// 404: body:NotFoundError
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
