package api

import (
	"database/sql"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	eventbus "durpdeploy/internal/events"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

func nullStringPtr(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

type AdminHandler struct {
	repo *repository.Repository
}

func NewAdminHandler(repo *repository.Repository) *AdminHandler {
	return &AdminHandler{repo: repo}
}

// swagger:route GET /admin/audit admin auditLog
//
// List audit log entries.
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:AuditLogListResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) AuditLog(w http.ResponseWriter, r *http.Request) {
	params := db.ListAuditLogEntriesParams{
		PageLimit: 1000,
	}
	if v := r.URL.Query().Get("user"); v != "" {
		params.FUser = sql.NullString{String: v, Valid: true}
	}

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n > 1000 {
			RespondError(
				w,
				http.StatusBadRequest,
				"limit must be a positive integer up to 1000",
			)
			return
		}
		params.PageLimit = n
	}
	if v := r.URL.Query().Get("user_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "user_id must be an integer")
			return
		}
		params.FUserID = sql.NullInt64{Int64: id, Valid: true}
	}
	if v := r.URL.Query().Get("action"); v != "" {
		params.FAction = sql.NullString{String: v, Valid: true}
	}
	if v := r.URL.Query().Get("entity_type"); v != "" {
		params.FEntityType = sql.NullString{String: v, Valid: true}
	}

	entries, err := h.repo.ListAuditLogEntries(r.Context(), params)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]any, len(entries))
	for i, e := range entries {
		items[i] = e
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  int64(len(entries)),
		Limit:  params.PageLimit,
		Offset: 0,
	})
}

// ListAuditLogs is an alias for AuditLog.
func (h *AdminHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	h.AuditLog(w, r)
}

// ListNotifications lists global notification events.
// swagger:route GET /admin/notifications admin listNotifications
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:NotificationEventListResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) ListNotifications(
	w http.ResponseWriter,
	r *http.Request,
) {
	events, err := h.repo.Queries.ListNotificationEvents(r.Context(), 1000)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range events {
		events[i].Results = eventbus.PublicNotificationResults(
			events[i].Results,
		)
	}
	RespondJSON(w, http.StatusOK, events)
}

// GetNotificationSettings returns global notification settings.
// swagger:route GET /admin/notifications/settings admin getNotificationSettings
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:GlobalNotification
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) GetNotificationSettings(
	w http.ResponseWriter,
	r *http.Request,
) {
	settings, err := h.repo.Queries.GetGlobalNotifications(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, newNotificationSettingsResponse(settings))
}

type notificationSettingsResponse struct {
	SlackWebhookURLConfigured   bool    `json:"slack_webhook_url_configured"`
	GotifyTokenConfigured       bool    `json:"gotify_token_configured"`
	DiscordWebhookURLConfigured bool    `json:"discord_webhook_url_configured"`
	ID                          int64   `json:"id"`
	SlackWebhookURL             *string `json:"slack_webhook_url"`
	NotifyEmails                *string `json:"notify_emails"`
	GotifyURL                   *string `json:"gotify_url"`
	GotifyToken                 *string `json:"gotify_token"`
	DiscordWebhookURL           *string `json:"discord_webhook_url"`
	UpdatedAt                   int64   `json:"updated_at"`
}

func newNotificationSettingsResponse(
	s db.GlobalNotification,
) notificationSettingsResponse {
	empty := ""
	return notificationSettingsResponse{
		ID:                          s.ID,
		SlackWebhookURL:             &empty,
		SlackWebhookURLConfigured:   s.SlackWebhookUrl.String != "",
		NotifyEmails:                nullStringPtr(s.NotifyEmails),
		GotifyURL:                   nullStringPtr(s.GotifyUrl),
		GotifyToken:                 &empty,
		GotifyTokenConfigured:       s.GotifyToken.String != "",
		DiscordWebhookURL:           &empty,
		DiscordWebhookURLConfigured: s.DiscordWebhookUrl.String != "",
		UpdatedAt:                   s.UpdatedAt,
	}
}

// UpdateNotificationSettings updates global notification settings.
// swagger:route PUT /admin/notifications/settings admin updateNotificationSettings
//
// Update global notification settings.
// An omitted or empty Gotify token is retained only for an unchanged Gotify URL.
// Changing or removing the URL clears the token unless a replacement is supplied.
// Explicit credential clear flags take precedence over replacements.
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
//	  200: body:GlobalNotification
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) UpdateNotificationSettings(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req projectNotificationsRequest
	if !readJSONBool(w, r, &req) {
		return
	}

	params, err := handler.PrepareNotificationSettings(
		handler.NotificationSettingsInput{
			SlackWebhookURL:        req.SlackWebhookURL,
			NotifyEmails:           req.NotifyEmails,
			GotifyURL:              trimSpace(req.GotifyURL),
			GotifyToken:            req.GotifyToken,
			DiscordWebhookURL:      req.DiscordWebhookURL,
			ClearSlackWebhookURL:   req.ClearSlackWebhookURL,
			ClearGotifyToken:       req.ClearGotifyToken,
			ClearDiscordWebhookURL: req.ClearDiscordWebhookURL,
		},
	)
	if err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	settings, err := h.repo.UpdateGlobalNotifications(
		r.Context(),
		params,
	)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not save notification settings",
		)
		return
	}
	RespondJSON(w, http.StatusOK, newNotificationSettingsResponse(settings))
}

// swagger:route GET /admin/stats admin stats
//
// Get server stats.
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:ServerStatsResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) Stats(w http.ResponseWriter, r *http.Request) {
	var users, projects, deployments int64
	err := h.repo.DB.QueryRowContext(r.Context(), `
SELECT (SELECT COUNT(*) FROM users),
       (SELECT COUNT(*) FROM projects),
       (SELECT COUNT(*) FROM deployments)
`).Scan(&users, &projects, &deployments)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, map[string]int64{
		"users":       users,
		"projects":    projects,
		"deployments": deployments,
	})
}

// swagger:route POST /admin/maintenance admin maintenance
//
// Diagnose deployment queue slots without changing cleanup evidence.
//
//	Security:
//	  bearer:
//
//	Responses:
//	  400: body:BadRequestError
//	  413: body:RequestEntityTooLargeError
//	  200: body:QueueMaintenanceResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) Maintenance(w http.ResponseWriter, r *http.Request) {
	slots, err := h.repo.DeploymentQueueDiagnostics(r.Context())
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not diagnose deployment queues",
		)
		return
	}
	RespondJSON(
		w,
		http.StatusOK,
		QueueMaintenanceResponse{Status: "ok", Slots: slots},
	)
}

// swagger:route GET /admin/db-tables admin dbTables
//
// List database tables.
//
//	Produces:
//	- application/json
//
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:DbTableListResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) DbTables(w http.ResponseWriter, r *http.Request) {
	query := "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"
	if _, err := h.repo.DB.ExecContext(
		r.Context(),
		"SELECT sqlite_version()",
	); err != nil {
		if r.Context().Err() != nil {
			RespondError(
				w,
				http.StatusInternalServerError,
				r.Context().Err().Error(),
			)
			return
		}
		query = "SELECT table_name FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('information_schema', 'sys') ORDER BY table_name"
	}
	rows, err := h.repo.DB.QueryContext(
		r.Context(),
		query,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	//nolint:errcheck // Early error exits retain the primary error below.
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := rows.Close(); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, tables)
}
