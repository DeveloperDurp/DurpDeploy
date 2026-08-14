package api

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type UserHandler struct {
	repo *repository.Repository
}

func NewUserHandler(repo *repository.Repository) *UserHandler {
	return &UserHandler{repo: repo}
}

func NewUsersHandler(repo *repository.Repository) *UserHandler {
	return NewUserHandler(repo)
}

type userRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type userResponse struct {
	ID              int64  `json:"id"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	OneTimePassword string `json:"one_time_password,omitempty"`
}

// swagger:route GET /admin/users admin-users adminListUsers
//
// List all users.
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
//	  200: body:UserListResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	rows, err := h.repo.Queries.ListUsersPaginated(
		r.Context(),
		db.ListUsersPaginatedParams{
			Limit:  limit,
			Offset: offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountUsers(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]userResponse, len(rows))
	for i, u := range rows {
		resp[i] = userResponse{
			ID:    u.ID,
			Email: u.Email,
			Name:  u.Name,
			Role:  u.Role,
		}
	}
	items := make([]any, len(resp))
	for i, ru := range resp {
		items[i] = ru
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /admin/users admin-users adminCreateUser
//
// Create a user.
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
//	  201: body:User
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  409: body:ConflictError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.Email == "" || req.Name == "" || req.Password == "" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"email, name, and password are required",
		)
		return
	}
	if !isValidRole(req.Role) {
		RespondError(w, http.StatusBadRequest, "invalid role")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	user, err := h.repo.Queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:        req.Email,
		Name:         req.Name,
		PasswordHash: hash,
		Role:         req.Role,
	})
	if err != nil {
		if isUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A user with this email already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusCreated, userResponse{
		ID:              user.ID,
		Email:           user.Email,
		Name:            user.Name,
		Role:            user.Role,
		OneTimePassword: req.Password,
	})
}

// swagger:route GET /admin/users/{id} admin-users adminGetUser
//
// Get a user.
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
//	  200: body:User
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	user, err := h.repo.Queries.GetUserByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "User not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(
		w,
		http.StatusOK,
		userResponse{
			ID:    user.ID,
			Email: user.Email,
			Name:  user.Name,
			Role:  user.Role,
		},
	)
}

// swagger:route PUT /admin/users/{id} admin-users adminUpdateUser
//
// Update a user.
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
//	  200: body:User
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *UserHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	var req userRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.Name == "" {
		RespondError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if req.Role != "" && !isValidRole(req.Role) {
		RespondError(w, http.StatusUnprocessableEntity, "invalid role")
		return
	}

	if err := h.repo.Queries.UpdateUser(r.Context(), db.UpdateUserParams{
		ID:   id,
		Name: req.Name,
		Role: req.Role,
	}); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.Password != "" {
		if err := auth.UpdatePassword(r.Context(), h.repo, auth.PasswordChange{
			UserID:   id,
			Password: req.Password,
		}); err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	user, err := h.repo.Queries.GetUserByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "User not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(
		w,
		http.StatusOK,
		userResponse{
			ID:    user.ID,
			Email: user.Email,
			Name:  user.Name,
			Role:  user.Role,
		},
	)
}

// swagger:route DELETE /admin/users/{id} admin-users adminDeleteUser
//
// Delete a user.
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
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	// Defense-in-depth: even though this route is admin-gated by
	// RequireRole("admin") on the aar sub-group, refuse to delete
	// the bearer token's own user. An admin who deletes themselves
	// from a CLI token still has a live session, but a 5-line check
	// is cheaper than a 3am "I just locked everyone out" page.
	if caller := auth.UserFromContext(
		r.Context(),
	); caller != nil &&
		caller.ID == id {
		RespondError(w, http.StatusForbidden, "cannot delete your own user")
		return
	}

	if err := h.repo.Queries.DeleteUser(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// swagger:route GET /users/me users getCurrentUser
//
// Get the currently authenticated user.
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
//	  200: body:User
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *UserHandler) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	RespondJSON(
		w,
		http.StatusOK,
		userResponse{
			ID:    user.ID,
			Email: user.Email,
			Name:  user.Name,
			Role:  user.Role,
		},
	)
}
