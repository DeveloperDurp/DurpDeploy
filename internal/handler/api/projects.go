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
	ClearSlackWebhookURL   bool   `json:"clear_slack_webhook_url"`
	ClearGotifyToken       bool   `json:"clear_gotify_token"`
	ClearDiscordWebhookURL bool   `json:"clear_discord_webhook_url"`
	SlackWebhookURL        string `json:"slack_webhook_url"`
	NotifyEmails           string `json:"notify_emails"`
	GotifyURL              string `json:"gotify_url"`
	GotifyToken            string `json:"gotify_token"`
	DiscordWebhookURL      string `json:"discord_webhook_url"`
}

type projectResponse struct {
	SlackWebhookURLConfigured   bool   `json:"slack_webhook_url_configured"`
	GotifyTokenConfigured       bool   `json:"gotify_token_configured"`
	DiscordWebhookURLConfigured bool   `json:"discord_webhook_url_configured"`
	ID                          int64  `json:"id"`
	Name                        string `json:"name"`
	Description                 string `json:"description"`
	CreatedAt                   int64  `json:"created_at"`
	LifecycleID                 *int64 `json:"lifecycle_id"`
	GroupID                     *int64 `json:"group_id"`
	SlackWebhookURL             string `json:"slack_webhook_url"`
	NotifyEmails                string `json:"notify_emails"`
	GotifyURL                   string `json:"gotify_url"`
	GotifyToken                 string `json:"gotify_token"`
	DiscordWebhookURL           string `json:"discord_webhook_url"`
}

func toProjectResponse(p db.Project) projectResponse {
	var lcID *int64
	var groupID *int64
	if p.GroupID.Valid {
		groupID = &p.GroupID.Int64
	}
	if p.LifecycleID.Valid {
		lcID = &p.LifecycleID.Int64
	}
	return projectResponse{
		ID:                          p.ID,
		Name:                        p.Name,
		Description:                 p.Description.String,
		CreatedAt:                   p.CreatedAt,
		LifecycleID:                 lcID,
		GroupID:                     groupID,
		SlackWebhookURL:             "",
		SlackWebhookURLConfigured:   p.SlackWebhookUrl.String != "",
		NotifyEmails:                p.NotifyEmails.String,
		GotifyURL:                   p.GotifyUrl.String,
		GotifyToken:                 "",
		GotifyTokenConfigured:       p.GotifyToken.String != "",
		DiscordWebhookURL:           "",
		DiscordWebhookURLConfigured: p.DiscordWebhookUrl.String != "",
	}
}

type projectNotificationResponse struct {
	SlackWebhookURLConfigured   bool   `json:"slack_webhook_url_configured"`
	GotifyTokenConfigured       bool   `json:"gotify_token_configured"`
	DiscordWebhookURLConfigured bool   `json:"discord_webhook_url_configured"`
	ID                          int64  `json:"id"`
	Name                        string `json:"name"`
	Description                 string `json:"description"`
	SlackWebhookURL             string `json:"slack_webhook_url"`
	NotifyEmails                string `json:"notify_emails"`
	GotifyURL                   string `json:"gotify_url"`
	GotifyToken                 string `json:"gotify_token"`
	DiscordWebhookURL           string `json:"discord_webhook_url"`
	CreatedAt                   int64  `json:"created_at"`
}

func toProjectNotificationResponse(p db.Project) projectNotificationResponse {
	return projectNotificationResponse{
		ID:                          p.ID,
		Name:                        p.Name,
		Description:                 p.Description.String,
		SlackWebhookURL:             "",
		SlackWebhookURLConfigured:   p.SlackWebhookUrl.String != "",
		NotifyEmails:                p.NotifyEmails.String,
		GotifyURL:                   p.GotifyUrl.String,
		GotifyToken:                 "",
		GotifyTokenConfigured:       p.GotifyToken.String != "",
		DiscordWebhookURL:           "",
		DiscordWebhookURLConfigured: p.DiscordWebhookUrl.String != "",
		CreatedAt:                   p.CreatedAt,
	}
}

// swagger:route GET /projects projects listProjects
//
// List projects the authenticated user can access.
//
//	Produces:
//	- application/json
//
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
	options, err := handler.ParseProjectCatalogOptions(r.URL.Query())
	if err != nil {
		RespondError(w, 400, err.Error())
		return
	}
	rows, total, err := h.repo.ProjectCatalog(
		r.Context(),
		handler.ProjectCatalogAccess(r),
		options,
	)
	if err != nil {
		RespondError(w, 500, "could not list projects")
		return
	}
	items := make([]any, len(rows))
	for i, project := range rows {
		items[i] = toProjectResponse(project)
	}

	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  options.Limit,
		Offset: options.Offset,
	})
}

// swagger:route POST /projects projects createProject
//
// Create a new project. Lifecycle assignment requires a global admin.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
//	  201: body:ProjectResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *ProjectHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if !readJSONBool(w, r, &req) {
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if !h.authorizeLifecycle(w, r, 0, req.LifecycleID) {
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
//
//	Security:
//	  bearer:
//
//	Responses:
//
// 413: body:RequestEntityTooLargeError
//
//	200: body:ProjectResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	404: body:NotFoundError
//	500: body:ServerError
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
// Update a project. Only a global admin can assign a different lifecycle.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
//	  200: body:ProjectResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  403: body:ForbiddenError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *ProjectHandler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req projectRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if !h.authorizeLifecycle(w, r, id, req.LifecycleID) {
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	updated, err := handler.UpdateProjectWithLifecycle(
		r.Context(),
		h.repo,
		db.UpdateProjectParams{
			ID:   id,
			Name: name,
			Description: sql.NullString{
				String: req.Description,
				Valid:  req.Description != "",
			},
		},
		req.LifecycleID,
	)
	if err != nil {
		if errors.Is(err, handler.ErrLifecycleAssignmentForbidden) ||
			errors.Is(err, handler.ErrLifecycleRemovalForbidden) {
			RespondError(w, http.StatusForbidden, err.Error())
			return
		}
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

	RespondJSON(w, http.StatusOK, toProjectResponse(updated))
}

// swagger:route DELETE /projects/{id} projects deleteProject
//
// Delete a project.
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
//	  204: body:EmptyResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *ProjectHandler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	if err := h.repo.DeleteProject(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrProjectHasActiveRunbook) {
			RespondError(w, http.StatusConflict, err.Error())
			return
		}
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
//
//	Security:
//	  bearer:
//
//	Responses:
//
// 413: body:RequestEntityTooLargeError
//
//	200: body:ProjectNotificationResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	404: body:NotFoundError
//	500: body:ServerError
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
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
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
	if !readJSONBool(w, r, &req) {
		return
	}

	webhook := notify.CredentialValue(
		req.SlackWebhookURL,
		req.ClearSlackWebhookURL,
	)
	gotifyURL := trimSpace(req.GotifyURL)
	discordWebhook := notify.CredentialValue(
		req.DiscordWebhookURL,
		req.ClearDiscordWebhookURL,
	)
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
			ClearSlackWebhookUrl:   boolInt(req.ClearSlackWebhookURL),
			ClearGotifyToken:       boolInt(req.ClearGotifyToken),
			ClearDiscordWebhookUrl: boolInt(req.ClearDiscordWebhookURL),
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
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not save notification settings",
		)
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

func (h *ProjectHandler) authorizeLifecycle(
	w http.ResponseWriter,
	r *http.Request,
	projectID, lifecycleID int64,
) bool {
	err := handler.AuthorizeLifecycleSelection(
		r.Context(), h.repo, projectID, lifecycleID,
	)
	if err == nil {
		return true
	}
	if errors.Is(err, handler.ErrLifecycleAssignmentForbidden) ||
		errors.Is(err, handler.ErrLifecycleRemovalForbidden) {
		RespondError(w, http.StatusForbidden, err.Error())
	} else {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not check lifecycle access",
		)
	}
	return false
}
