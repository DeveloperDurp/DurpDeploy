package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type ProjectTemplateHandler struct{ repo *repository.Repository }

type saveProjectTemplateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func NewProjectTemplateHandler(
	repo *repository.Repository,
) *ProjectTemplateHandler {
	return &ProjectTemplateHandler{repo: repo}
}

// swagger:model ProjectTemplateResponse
type projectTemplateResponse struct {
	ID              int64                              `json:"id"`
	SourceProjectID int64                              `json:"source_project_id"`
	Name            string                             `json:"name"`
	Description     string                             `json:"description"`
	CreatedAt       int64                              `json:"created_at"`
	Snapshot        repository.ProjectTemplateSnapshot `json:"snapshot"`
}

// swagger:route GET /project-templates project-templates listProjectTemplates
// List project templates accessible through current source-project membership.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:ProjectTemplateListResponse
//	403: body:ForbiddenError
func (h *ProjectTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	projectID := int64(0)
	if value, _ := auth.ProjectIDFromContext(r.Context()); value != 0 {
		projectID = value
	}
	actor := handler.ProjectTemplateActor(r)
	admin := int64(0)
	if user := auth.UserFromContext(
		r.Context(),
	); user != nil &&
		user.Role == "admin" {
		admin = 1
	}
	rows, err := h.repo.Queries.ListAccessibleProjectTemplates(
		r.Context(),
		db.ListAccessibleProjectTemplatesParams{
			SourceProjectID: projectID,
			UserID:          actor.UserID,
			IsAdmin:         admin,
			PageLimit:       limit,
			PageOffset:      offset,
		},
	)
	if err != nil {
		projectTemplateError(w, err)
		return
	}
	total, err := h.repo.Queries.CountAccessibleProjectTemplates(
		r.Context(),
		db.CountAccessibleProjectTemplatesParams{
			SourceProjectID: projectID,
			UserID:          actor.UserID,
			IsAdmin:         admin,
		},
	)
	if err != nil {
		projectTemplateError(w, err)
		return
	}
	if rows == nil {
		rows = []db.ListAccessibleProjectTemplatesRow{}
	}
	items := make([]any, len(rows))
	for i, row := range rows {
		items[i] = row
	}
	RespondJSON(
		w,
		http.StatusOK,
		PaginatedResponse{
			Items:  items,
			Total:  total,
			Limit:  limit,
			Offset: offset,
		},
	)
}

// swagger:route POST /projects/{id}/project-templates project-templates saveProjectTemplate
// Save an independent project snapshot without secret values or credentials.
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
//	201: body:ProjectTemplateResponse
//	403: body:ForbiddenError
//	409: body:ConflictError
//	422: body:ValidationError
func (h *ProjectTemplateHandler) Save(w http.ResponseWriter, r *http.Request) {
	var input saveProjectTemplateRequest
	if !readJSONBool(w, r, &input) {
		return
	}
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	row, err := h.repo.SaveProjectTemplate(
		r.Context(),
		handler.ProjectTemplateActor(r),
		projectID,
		input.Name,
		input.Description,
	)
	if err != nil {
		projectTemplateError(w, err)
		return
	}
	audit.SetProjectTemplateResult(
		r,
		"create_project_template",
		"project_template",
		row.ID,
	)
	h.respond(w, r, row, http.StatusCreated)
}

// swagger:route GET /projects/{id}/project-templates/{templateId} project-templates previewProjectTemplate
// Preview copied defaults, secret definitions, steps, and references.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:ProjectTemplateResponse
//	403: body:ForbiddenError
//	404: body:NotFoundError
func (h *ProjectTemplateHandler) Preview(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	id, err := parseParamInt(r, "templateId")
	if err != nil || id <= 0 {
		RespondError(w, 400, "Invalid template ID")
		return
	}
	row, err := h.repo.Queries.GetProjectTemplate(
		r.Context(),
		db.GetProjectTemplateParams{SourceProjectID: projectID, ID: id},
	)
	if err != nil {
		projectTemplateError(w, err)
		return
	}
	h.respond(w, r, row, http.StatusOK)
}

func (h *ProjectTemplateHandler) respond(
	w http.ResponseWriter,
	r *http.Request,
	row db.ProjectTemplate,
	status int,
) {
	snapshot, err := h.repo.ReadProjectTemplate(row)
	if err != nil {
		projectTemplateError(w, err)
		return
	}
	RespondJSON(
		w,
		status,
		projectTemplateResponse{
			ID:              row.ID,
			SourceProjectID: row.SourceProjectID,
			Name:            row.Name,
			Description:     row.Description,
			CreatedAt:       row.CreatedAt,
			Snapshot:        snapshot,
		},
	)
}

// swagger:route POST /projects/{id}/project-templates/{templateId}/projects project-templates applyProjectTemplate
// Create a project atomically with explicit mappings and fresh secrets and credentials.
// Scoped tokens cannot create projects. Lifecycle assignment requires a global administrator.
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
//	201: body:ProjectResponse
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	422: body:ValidationError
func (h *ProjectTemplateHandler) Apply(w http.ResponseWriter, r *http.Request) {
	var input repository.ApplyProjectTemplate
	if !readJSONBool(w, r, &input) {
		return
	}
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	id, err := parseParamInt(r, "templateId")
	if err != nil || id <= 0 {
		RespondError(w, 400, "Invalid template ID")
		return
	}
	created, err := h.repo.CreateProjectFromTemplate(
		r.Context(),
		handler.ProjectTemplateActor(r),
		projectID,
		id,
		input,
	)
	if err != nil {
		projectTemplateError(w, err)
		return
	}
	audit.SetProjectTemplateResult(
		r,
		"create_project_from_template",
		"project",
		created.ID,
	)
	RespondJSON(w, http.StatusCreated, toProjectResponse(created))
}

// swagger:route GET /projects/{id}/project-templates project-templates listSourceProjectTemplates
// List templates owned by an accessible source project.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:ProjectTemplateListResponse
//	403: body:ForbiddenError
func (h *ProjectTemplateHandler) ListForProject(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.List(w, r)
}

func projectTemplateError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "Could not process project template"
	switch {
	case errors.Is(err, repository.ErrProjectTemplateAccess):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, repository.ErrProjectTemplateInvalid):
		status, message = http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, sql.ErrNoRows):
		status, message = http.StatusNotFound, "Project template not found"
	case handler.IsUniqueViolation(err):
		status, message = http.StatusConflict, "A project or template with this name already exists"
	}
	RespondError(w, status, message)
}

// swagger:model ProjectTemplateListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectTemplateListResponse struct {
	Items  []db.ListAccessibleProjectTemplatesRow `json:"items"`
	Total  int64                                  `json:"total"`
	Limit  int64                                  `json:"limit"`
	Offset int64                                  `json:"offset"`
}
