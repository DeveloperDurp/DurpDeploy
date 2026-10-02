package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type ArtifactHandler struct{ repo *repository.Repository }

func NewArtifactHandler(repo *repository.Repository) *ArtifactHandler {
	return &ArtifactHandler{repo}
}

// swagger:model PackageRepositoryRequest
type packageRepositoryRequest struct {
	URLTemplate string `json:"url_template"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username"`
	Credential  string `json:"credential"`
}

// swagger:model PackageRepositoryResponse
type packageRepositoryResponse struct {
	ID                   int64  `json:"id"`
	ProjectID            int64  `json:"project_id"`
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
	if errors.Is(err, sql.ErrNoRows) {
		RespondError(w, 404, "Package repository or pin not found")
		return
	}
	RespondError(w, 500, "Cannot access package repository")
}

func writeArtifactError(w http.ResponseWriter, err error) bool {
	status := handler.ArtifactErrorStatus(err)
	if status == 0 {
		return false
	}
	RespondError(w, status, err.Error())
	return true
}

// swagger:route GET /projects/{id}/package-repository artifacts getPackageRepository
// Read the active project repository, or null when no package is configured.
// Responses:
// 200: body:PackageRepositoryResponse
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 500: body:ServerError
func (h *ArtifactHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	row, err := h.repo.Queries.GetProjectArtifactRepository(
		r.Context(),
		projectID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		RespondJSON(w, 200, nil)
		return
	}
	if err != nil {
		artifactError(w, err)
		return
	}
	RespondJSON(w, 200, publicPackageRepository(row))
}

// swagger:route PUT /projects/{id}/package-repository artifacts savePackageRepository
// Configure and activate the project repository. Replacing its source preserves existing pins.
// A blank credential preserves the secret only when URL template, auth type, and username are unchanged.
// Responses:
// 200: body:PackageRepositoryResponse
// 400: body:BadRequestError
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 422: body:ValidationError
// 500: body:ServerError
func (h *ArtifactHandler) Save(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	var req packageRepositoryRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	row, err := h.repo.SaveProjectPackageRepository(
		r.Context(),
		projectID,
		artifact.Repository{
			URLTemplate: req.URLTemplate,
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

// swagger:route DELETE /projects/{id}/package-repository artifacts removePackageRepository
// Disable packages for future snapshots. Source records needed by existing pins are retained.
// Responses:
// 204: description: Removed
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 500: body:ServerError
func (h *ArtifactHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	if err := h.repo.RemoveProjectPackageRepository(
		r.Context(),
		projectID,
	); err != nil {
		artifactError(w, err)
		return
	}
	w.WriteHeader(204)
}

// swagger:model PackageTestRequest
type packageTestRequest struct {
	Version string `json:"version"`
}

// swagger:model PackageTestResponse
type packageTestResponse struct {
	Exists  bool   `json:"exists"`
	URL     string `json:"url"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// swagger:route POST /projects/{id}/package-repository/test artifacts testPackageRepository
// Download and validate one version using the saved repository credentials, without creating a release.
// Size is the compressed ZIP byte count. Failure does not claim that the package is missing.
// Responses:
// 200: body:PackageTestResponse
// 400: body:BadRequestError
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 422: body:ValidationError
// 500: body:ServerError
// 502: body:ServerError
func (h *ArtifactHandler) Test(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	var req packageTestRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	pin, err := h.repo.TestProjectPackage(r.Context(), projectID, req.Version)
	if err != nil {
		artifactError(w, err)
		return
	}
	RespondJSON(
		w,
		200,
		packageTestResponse{true, pin.URL, pin.Version, pin.SHA256, pin.Size},
	)
}
