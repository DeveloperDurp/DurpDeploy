package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func canManageMembers(
	ctx context.Context,
	repo *repository.Repository,
	userID, projectID int64,
) bool {
	user := auth.UserFromContext(ctx)
	if user != nil && user.Role == "admin" {
		return true
	}
	member, err := repo.Queries.GetProjectMember(ctx, db.GetProjectMemberParams{
		ProjectID: projectID,
		UserID:    userID,
	})
	if err != nil {
		return false
	}
	return member.Role == "admin"
}

type MemberHandler struct {
	repo *repository.Repository
}

func NewMemberHandler(repo *repository.Repository) *MemberHandler {
	return &MemberHandler{repo: repo}
}

func NewProjectMemberHandler(repo *repository.Repository) *MemberHandler {
	return NewMemberHandler(repo)
}

type projectMemberRequest struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

// swagger:route GET /projects/{id}/members members listMembers
//
// List members of a project.
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
//	  200: body:ProjectMemberListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *MemberHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
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

	members, err := h.repo.Queries.ListProjectMembersPaginated(
		r.Context(),
		db.ListProjectMembersPaginatedParams{
			ProjectID: projectID,
			Limit:     limit,
			Offset:    offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountProjectMembers(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(members))
	for i, m := range members {
		items[i] = m
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /projects/{id}/members members addMember
//
// Add a member to a project.
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
//	  201: body:ProjectMember
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *MemberHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req projectMemberRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.UserID == 0 || req.Role == "" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"user_id and role are required",
		)
		return
	}
	if req.Role != "admin" && req.Role != "deployer" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"role must be admin or deployer",
		)
		return
	}

	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	if !canManageMembers(r.Context(), h.repo, user.ID, projectID) {
		RespondError(w, http.StatusForbidden, "Admin access required")
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
	if _, err := h.repo.Queries.GetUserByID(
		r.Context(),
		req.UserID,
	); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusBadRequest, "User not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.repo.Queries.AddProjectMember(
		r.Context(),
		db.AddProjectMemberParams{
			ProjectID: projectID,
			UserID:    req.UserID,
			Role:      req.Role,
		},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	member, err := h.repo.Queries.GetProjectMember(
		r.Context(),
		db.GetProjectMemberParams{
			ProjectID: projectID,
			UserID:    req.UserID,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusCreated, member)
}

// swagger:route DELETE /projects/{id}/members/{userId} members removeMember
//
// Remove a member from a project.
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  204: body:EmptyResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *MemberHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}
	userID, err := strconv.ParseInt(chi.URLParam(r, "userId"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	if !canManageMembers(r.Context(), h.repo, user.ID, projectID) {
		RespondError(w, http.StatusForbidden, "Admin access required")
		return
	}

	if err := h.repo.Queries.RemoveProjectMember(
		r.Context(),
		db.RemoveProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
		},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// swagger:route PUT /projects/{id}/members/{userId} members updateMemberRole
//
// Change a project member's role.
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
//	  200: body:ProjectMember
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *MemberHandler) UpdateMemberRole(
	w http.ResponseWriter,
	r *http.Request,
) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}
	userID, err := strconv.ParseInt(chi.URLParam(r, "userId"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	if !canManageMembers(r.Context(), h.repo, user.ID, projectID) {
		RespondError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.Role != "admin" && req.Role != "deployer" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"role must be admin or deployer",
		)
		return
	}

	if _, err := h.repo.Queries.GetProjectMember(
		r.Context(),
		db.GetProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
		},
	); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Member not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.repo.Queries.AddProjectMember(
		r.Context(),
		db.AddProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
			Role:      req.Role,
		},
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	member, err := h.repo.Queries.GetProjectMember(
		r.Context(),
		db.GetProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, member)
}
