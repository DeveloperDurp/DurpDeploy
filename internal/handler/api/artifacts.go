package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type ArtifactHandler struct{ repo *repository.Repository }

func NewArtifactHandler(
	repo *repository.Repository,
) *ArtifactHandler {
	return &ArtifactHandler{repo}
}

// swagger:model PackageRepositoryRequest
type packageRepositoryRequest struct {
	Name        string `json:"name"`
	URLTemplate string `json:"url_template"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username"`
	Credential  string `json:"credential"`
}

// swagger:model PackageRepositoryResponse
type packageRepositoryResponse struct {
	ID                   int64  `json:"id"`
	ProjectID            int64  `json:"project_id"`
	Name                 string `json:"name"`
	URLTemplate          string `json:"url_template"`
	AuthType             string `json:"auth_type"`
	Username             string `json:"username"`
	CredentialConfigured bool   `json:"credential_configured"`
}

func publicPackageRepository(
	row db.PackageRepository,
) packageRepositoryResponse {
	return packageRepositoryResponse{
		row.ID,
		row.ProjectID,
		row.Name,
		row.UrlTemplate,
		row.AuthType,
		row.Username,
		row.Credential != "",
	}
}

func artifactError(w http.ResponseWriter, err error) {
	if status := handler.ArtifactErrorStatus(err); status != 0 {
		RespondError(w, status, err.Error())
		return
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		RespondError(w, 404, "Artifact repository or pin not found")
	case isUniqueViolation(err):
		RespondError(w, 409, "Repository is pinned or its name already exists")
	default:
		RespondError(w, 500, "Cannot update artifact repository")
	}
}

func writeArtifactError(w http.ResponseWriter, err error) bool {
	status := handler.ArtifactErrorStatus(err)
	if status == 0 {
		return false
	}
	RespondError(w, status, err.Error())
	return true
}

// swagger:route GET /projects/{id}/package-repositories artifacts listPackageRepositories
// List generic ZIP repositories without credentials.
// Responses:
// 200: body:PackageRepositoryListResponse
func (h *ArtifactHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Queries.ListPackageRepositories(r.Context(), projectID)
	if err != nil {
		artifactError(w, err)
		return
	}
	items := make([]packageRepositoryResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, publicPackageRepository(row))
	}
	RespondJSON(w, 200, items)
}

// swagger:route GET /projects/{id}/package-repositories/{repositoryId} artifacts getPackageRepository
// Read a repository without its credential.
// Responses:
// 200: body:PackageRepositoryResponse
func (h *ArtifactHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "repositoryId")
	if !ok {
		return
	}
	row, err := h.repo.Queries.GetPackageRepository(r.Context(), id)
	if err == nil && row.ProjectID != projectID {
		err = sql.ErrNoRows
	}
	if err != nil {
		artifactError(w, err)
		return
	}
	RespondJSON(w, 200, publicPackageRepository(row))
}

// swagger:route POST /projects/{id}/package-repositories artifacts createPackageRepository
// Create an HTTPS generic ZIP repository.
// Responses:
// 201: body:PackageRepositoryResponse
// 422: body:ValidationError
func (h *ArtifactHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	var req packageRepositoryRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	row, err := h.repo.CreatePackageRepository(
		r.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID:   projectID,
			Name:        req.Name,
			UrlTemplate: req.URLTemplate,
			AuthType:    req.AuthType,
			Username:    req.Username,
			Credential:  req.Credential,
		},
	)
	if err != nil {
		artifactError(w, err)
		return
	}
	RespondJSON(w, 201, publicPackageRepository(row))
}

// swagger:route PUT /projects/{id}/package-repositories/{repositoryId} artifacts updatePackageRepository
// Update a repository. A blank credential preserves the current secret.
// Responses:
// 200: body:PackageRepositoryResponse
// 409: body:ConflictError
func (h *ArtifactHandler) Update(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "repositoryId")
	if !ok {
		return
	}
	var req packageRepositoryRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	row, err := h.repo.UpdatePackageRepository(
		r.Context(),
		db.UpdatePackageRepositoryParams{
			ID:          id,
			ProjectID:   projectID,
			Name:        req.Name,
			UrlTemplate: req.URLTemplate,
			AuthType:    req.AuthType,
			Username:    req.Username,
			Credential:  req.Credential,
		},
	)
	if err != nil {
		artifactError(w, err)
		return
	}
	RespondJSON(w, 200, publicPackageRepository(row))
}

// swagger:route DELETE /projects/{id}/package-repositories/{repositoryId} artifacts deletePackageRepository
// Delete an unpinned repository.
// Responses:
// 204: description: Deleted
// 409: body:ConflictError
func (h *ArtifactHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "repositoryId")
	if !ok {
		return
	}
	if err := h.repo.DeletePackageRepository(
		r.Context(),
		db.DeletePackageRepositoryParams{ID: id, ProjectID: projectID},
	); err != nil {
		artifactError(w, err)
		return
	}
	w.WriteHeader(204)
}
