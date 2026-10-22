package api

import (
	"encoding/json"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/handler"
)

// swagger:model ProjectGroup
type projectGroupResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// swagger:model ProjectGroupRequest
type projectGroupRequest struct {
	Name string `json:"name"`
}

// swagger:model ProjectGroupAssignment
//
//nolint:unused // Used by Swagger specification generation.
type projectGroupAssignment struct {
	// Send null to move the project to Ungrouped. This field is required.
	// required: true
	// Extensions:
	// ---
	// x-nullable: true
	GroupID *int64 `json:"group_id"`
}

// swagger:route GET /project-groups project-groups listProjectGroups
//
// List groups containing accessible projects. Global administrators also see empty groups.
// Scoped tokens see only groups containing selected, accessible projects.
//
// Produces:
// - application/json
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:ProjectGroupListResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	500: body:ServerError
func (h *ProjectHandler) ListProjectGroups(
	w http.ResponseWriter,
	r *http.Request,
) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	groups, err := h.repo.Queries.ListAccessibleProjectGroups(
		r.Context(),
		handler.ProjectCatalogAccess(r),
	)
	if err != nil {
		RespondError(w, 500, "could not list groups")
		return
	}
	rows := tokenPage(groups, limit, offset)
	items := make([]any, len(rows))
	for i, group := range rows {
		items[i] = group
	}
	RespondJSON(
		w,
		200,
		PaginatedResponse{
			Items:  items,
			Total:  int64(len(groups)),
			Limit:  limit,
			Offset: offset,
		},
	)
}

// swagger:route POST /project-groups project-groups createProjectGroup
//
// Create a shared group. Requires a global administrator and a global token with an Admin ceiling.
// Names are trimmed, case-insensitively unique, and limited to 100 characters. Ungrouped is reserved.
//
// Consumes:
// - application/json
// Produces:
// - application/json
// Security:
//
//	bearer:
//
// Responses:
//
//	201: body:ProjectGroup
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	409: body:ConflictError
//	422: body:ValidationError
//	500: body:ServerError
func (h *ProjectHandler) CreateProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.saveProjectGroup(w, r, 0, http.StatusCreated)
}

// swagger:route PUT /project-groups/{id} project-groups updateProjectGroup
//
// Rename a shared group. Requires a global administrator and a global token with an Admin ceiling.
//
// Consumes:
// - application/json
// Produces:
// - application/json
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:ProjectGroup
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	422: body:ValidationError
//	500: body:ServerError
func (h *ProjectHandler) UpdateProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}
	if id <= 0 {
		RespondError(w, 400, "Invalid group ID")
		return
	}
	h.saveProjectGroup(w, r, id, http.StatusOK)
}

func (h *ProjectHandler) saveProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
	id int64,
	status int,
) {
	var body projectGroupRequest
	if !readJSONBool(w, r, &body) {
		return
	}
	group, err := h.repo.SaveProjectGroup(r.Context(), id, body.Name)
	if err != nil {
		RespondError(w, handler.ProjectGroupErrorStatus(err), err.Error())
		return
	}
	audit.SetProjectGroupChange(r, group.ID, group.Name, nil)
	RespondJSON(w, status, projectGroupResponse{ID: group.ID, Name: group.Name})
}

// swagger:route DELETE /project-groups/{id} project-groups deleteProjectGroup
//
// Delete a group and move its projects to Ungrouped. Requires a global administrator and global Admin token.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	204: description:Deleted
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	500: body:ServerError
func (h *ProjectHandler) DeleteProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}
	if id <= 0 {
		RespondError(w, 400, "Invalid group ID")
		return
	}
	deleted, err := h.repo.Queries.DeleteProjectGroup(r.Context(), id)
	if err != nil {
		RespondError(w, 500, "could not delete group")
		return
	}
	if deleted == 0 {
		RespondError(w, 404, "group not found")
		return
	}
	audit.SetProjectGroupChange(r, id, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

// swagger:route PUT /projects/{id}/group projects moveProjectGroup
//
// Assign a project to a visible group, or send group_id null for Ungrouped.
// Requires project authoring permission. Group membership does not grant project access.
//
// Consumes:
// - application/json
// Produces:
// - application/json
// Security:
//
//	bearer:
//
// Responses:
//
//	200: body:ProjectResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	422: body:ValidationError
//	500: body:ServerError
func (h *ProjectHandler) MoveProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	var body struct {
		GroupID json.RawMessage `json:"group_id"`
	}
	if !readJSONBool(w, r, &body) {
		return
	}
	var groupID *int64
	if len(body.GroupID) == 0 ||
		json.Unmarshal(body.GroupID, &groupID) != nil ||
		groupID != nil && *groupID <= 0 {
		RespondError(w, 422, "group_id must be a positive integer or null")
		return
	}
	project, err := h.repo.MoveProjectGroup(
		r.Context(),
		handler.ProjectCatalogAccess(r),
		id,
		groupID,
	)
	if err != nil {
		RespondError(
			w,
			handler.ProjectGroupErrorStatus(err),
			"could not move project",
		)
		return
	}
	audit.SetProjectGroupChange(r, id, "", groupID)
	RespondJSON(w, 200, toProjectResponse(project))
}
