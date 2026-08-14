package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/notify"
	"durpdeploy/internal/repository"
)

type ProjectHandler struct {
	repo *repository.Repository
}

func NewProjectHandler(repo *repository.Repository) *ProjectHandler {
	return &ProjectHandler{repo: repo}
}

type projectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LifecycleID int64  `json:"lifecycle_id"`
}

type projectNotificationsRequest struct {
	SlackWebhookURL   string `json:"slack_webhook_url"`
	NotifyEmails      string `json:"notify_emails"`
	GotifyURL         string `json:"gotify_url"`
	GotifyToken       string `json:"gotify_token"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
}

type projectResponse struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	CreatedAt         int64  `json:"created_at"`
	LifecycleID       *int64 `json:"lifecycle_id"`
	SlackWebhookURL   string `json:"slack_webhook_url"`
	NotifyEmails      string `json:"notify_emails"`
	GotifyURL         string `json:"gotify_url"`
	GotifyToken       string `json:"gotify_token"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
}

func toProjectResponse(p db.Project) projectResponse {
	var lcID *int64
	if p.LifecycleID.Valid {
		lcID = &p.LifecycleID.Int64
	}
	return projectResponse{
		ID:                p.ID,
		Name:              p.Name,
		Description:       p.Description.String,
		CreatedAt:         p.CreatedAt,
		LifecycleID:       lcID,
		SlackWebhookURL:   p.SlackWebhookUrl.String,
		NotifyEmails:      p.NotifyEmails.String,
		GotifyURL:         p.GotifyUrl.String,
		GotifyToken:       p.GotifyToken.String,
		DiscordWebhookURL: p.DiscordWebhookUrl.String,
	}
}

type projectNotificationResponse struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	SlackWebhookURL   string `json:"slack_webhook_url"`
	NotifyEmails      string `json:"notify_emails"`
	GotifyURL         string `json:"gotify_url"`
	GotifyToken       string `json:"gotify_token"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
	CreatedAt         int64  `json:"created_at"`
}

func toProjectNotificationResponse(p db.Project) projectNotificationResponse {
	return projectNotificationResponse{
		ID:                p.ID,
		Name:              p.Name,
		Description:       p.Description.String,
		SlackWebhookURL:   p.SlackWebhookUrl.String,
		NotifyEmails:      p.NotifyEmails.String,
		GotifyURL:         p.GotifyUrl.String,
		GotifyToken:       p.GotifyToken.String,
		DiscordWebhookURL: p.DiscordWebhookUrl.String,
		CreatedAt:         p.CreatedAt,
	}
}

// swagger:route GET /projects projects listProjects
//
// List projects the authenticated user can access.
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
//	  200: body:ProjectListResponse
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *ProjectHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondJSON(
			w,
			http.StatusUnauthorized,
			map[string]string{"error": "unauthorized"},
		)
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	var items []any
	var total int64
	if user.Role == "admin" {
		rows, err := h.repo.Queries.ListProjectsPaginated(
			r.Context(),
			db.ListProjectsPaginatedParams{
				Limit:  limit,
				Offset: offset,
			},
		)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		total, err = h.repo.Queries.CountProjects(r.Context())
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = make([]any, len(rows))
		for i, p := range rows {
			items[i] = toProjectResponse(p)
		}
	} else {
		rows, err := h.repo.Queries.ListProjectsForUserPaginated(
			r.Context(),
			db.ListProjectsForUserPaginatedParams{
				UserID: user.ID,
				Limit:  limit,
				Offset: offset,
			},
		)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		total, err = h.repo.Queries.CountProjectsForUser(r.Context(), user.ID)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = make([]any, len(rows))
		for i, p := range rows {
			items[i] = toProjectResponse(p)
		}
	}

	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /projects projects createProject
//
// Create a new project.
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
//	  201: body:ProjectResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *ProjectHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	params := db.CreateProjectParams{
		Name: name,
		Description: sql.NullString{
			String: req.Description,
			Valid:  req.Description != "",
		},
	}

	var created db.Project
	err := h.repo.WithTx(r.Context(), func(q *db.Queries) error {
		var txErr error
		created, txErr = q.CreateProject(r.Context(), params)
		if txErr != nil {
			return txErr
		}
		if user := auth.UserFromContext(r.Context()); user != nil {
			txErr = q.AddProjectMember(
				r.Context(),
				db.AddProjectMemberParams{
					ProjectID: created.ID,
					UserID:    user.ID,
					Role:      "admin",
				},
			)
		}
		return txErr
	})
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A project with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := handler.ApplyLifecycleSelection(
		r.Context(),
		h.repo,
		created.ID,
		req.LifecycleID,
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	created, err = h.repo.Queries.GetProject(r.Context(), created.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, toProjectResponse(created))
}

// swagger:route GET /projects/{id} projects getProject
//
// Get a project by ID.
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
//	  200: body:ProjectResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ProjectHandler) GetProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Project not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, toProjectResponse(project))
}

// swagger:route PUT /projects/{id} projects updateProject
//
// Update a project.
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
//	  200: body:ProjectResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *ProjectHandler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req projectRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	updated, err := h.repo.Queries.UpdateProject(
		r.Context(),
		db.UpdateProjectParams{
			ID:   id,
			Name: name,
			Description: sql.NullString{
				String: req.Description,
				Valid:  req.Description != "",
			},
		},
	)
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A project with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := handler.ApplyLifecycleSelection(
		r.Context(),
		h.repo,
		id,
		req.LifecycleID,
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err = h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, toProjectResponse(updated))
}

// swagger:route DELETE /projects/{id} projects deleteProject
//
// Delete a project.
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
//	  500: body:ServerError
func (h *ProjectHandler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	if err := h.repo.Queries.DeleteProject(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// swagger:route GET /projects/{id}/notifications projects getProjectNotifications
//
// Get project notification settings.
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
//	  200: body:ProjectNotificationResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ProjectHandler) GetProjectNotifications(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Project not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, toProjectNotificationResponse(project))
}

// swagger:route PUT /projects/{id}/notifications projects updateProjectNotifications
//
// Update project notification settings.
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
//	  200: body:ProjectNotificationResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ProjectHandler) UpdateProjectNotifications(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req projectNotificationsRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	webhook := trimSpace(req.SlackWebhookURL)
	gotifyURL := trimSpace(req.GotifyURL)
	discordWebhook := trimSpace(req.DiscordWebhookURL)
	if err := validateNotificationURLs(
		webhook,
		gotifyURL,
		discordWebhook,
	); err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = h.repo.Queries.UpdateProjectNotifications(
		r.Context(),
		db.UpdateProjectNotificationsParams{
			SlackWebhookUrl: sql.NullString{
				String: webhook,
				Valid:  webhook != "",
			},
			NotifyEmails: sql.NullString{
				String: trimSpace(req.NotifyEmails),
				Valid:  trimSpace(req.NotifyEmails) != "",
			},
			GotifyUrl: sql.NullString{
				String: gotifyURL,
				Valid:  gotifyURL != "",
			},
			GotifyToken: sql.NullString{
				String: trimSpace(req.GotifyToken),
				Valid:  trimSpace(req.GotifyToken) != "",
			},
			DiscordWebhookUrl: sql.NullString{
				String: discordWebhook,
				Valid:  discordWebhook != "",
			},
			ID: id,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, toProjectNotificationResponse(project))
}

func validateNotificationURLs(urls ...string) error {
	for _, raw := range urls {
		if err := notify.ValidateEndpointURL(raw); err != nil {
			return err
		}
	}
	return nil
}
