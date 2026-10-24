package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type TemplateLibraryHandler struct {
	repo *repository.Repository
}

func NewTemplateLibraryHandler(
	repo *repository.Repository,
) *TemplateLibraryHandler {
	return &TemplateLibraryHandler{repo: repo}
}

// swagger:model LibraryTemplateListResponse
type LibraryTemplateListResponse struct {
	Items  []repository.LibraryTemplate `json:"items"`
	Total  int64                        `json:"total"`
	Limit  int64                        `json:"limit"`
	Offset int64                        `json:"offset"`
}

// swagger:route GET /template-library template-library listLibraryTemplates
// List curated step and workflow templates. Global tokens only.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:LibraryTemplateListResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
func (h *TemplateLibraryHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.projectExists(w, r) {
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	entries := repository.TemplateLibrary()
	total := int64(len(entries))
	start := min(offset, total)
	end := start + min(limit, total-start)
	RespondJSON(w, http.StatusOK, LibraryTemplateListResponse{
		Items:  entries[start:end],
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route GET /template-library/{slug} template-library previewLibraryTemplate
// Preview every script, variable, dependency, and permission. Global tokens only.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:LibraryTemplate
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
func (h *TemplateLibraryHandler) Preview(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !h.projectExists(w, r) {
		return
	}
	entry, err := repository.GetLibraryTemplate(chi.URLParam(r, "slug"))
	if err != nil {
		libraryError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, entry)
}

func (h *TemplateLibraryHandler) projectExists(
	w http.ResponseWriter,
	r *http.Request,
) bool {
	if projectID, ok := auth.ProjectIDFromContext(r.Context()); ok {
		if _, err := h.repo.Queries.GetProject(r.Context(), projectID); err != nil {
			libraryError(w, err)
			return false
		}
	}
	return true
}

// swagger:route GET /projects/{id}/template-library template-library listProjectLibraryTemplates
// Browse curated templates within an authorized project. Supports scoped tokens.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:LibraryTemplateListResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
func (h *TemplateLibraryHandler) ListForProject(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.List(w, r)
}

// swagger:route GET /projects/{id}/template-library/{slug} template-library previewProjectLibraryTemplate
// Preview a curated template within an authorized project. Supports scoped tokens.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:LibraryTemplate
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
func (h *TemplateLibraryHandler) PreviewForProject(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.Preview(w, r)
}

// swagger:route POST /projects/{id}/template-library/{slug}/apply template-library applyLibraryTemplate
// Append editable steps and encrypted variables atomically. Never starts work.
// Requires effective Maintainer permission. Conflicts do not overwrite configuration.
//
// Consumes:
//   - application/json
//
// Produces:
//   - application/json
//
// Security:
//
//	bearer:
//
// Responses:
//
//	201: body:LibraryApplyResult
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	413: body:RequestEntityTooLargeError
//	422: body:ValidationError
func (h *TemplateLibraryHandler) Apply(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	var input repository.ApplyLibraryTemplate
	if !readJSONBool(w, r, &input) {
		return
	}
	actor := handler.ProjectTemplateActor(r)
	slug := chi.URLParam(r, "slug")
	result, err := h.repo.ApplyLibraryTemplate(
		r.Context(),
		actor,
		projectID,
		slug,
		input,
	)
	if err != nil {
		libraryError(w, err)
		return
	}
	audit.SetLibraryTemplateResult(r, result)
	RespondJSON(w, http.StatusCreated, result)
}

func libraryError(w http.ResponseWriter, err error) {
	status, message := handler.LibraryTemplateError(err)
	RespondError(w, status, message)
}
