package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
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
//	Schemes: http, https
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
	params := db.ListAuditLogsFilteredParams{
		PageLimit: 1000,
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

	entries, err := h.repo.Queries.ListAuditLogsFiltered(r.Context(), params)
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
//	Schemes: http, https
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
	RespondJSON(w, http.StatusOK, events)
}

// GetNotificationSettings returns global notification settings.
// swagger:route GET /admin/notifications/settings admin getNotificationSettings
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
	ID                int64   `json:"id"`
	SlackWebhookURL   *string `json:"slack_webhook_url"`
	NotifyEmails      *string `json:"notify_emails"`
	GotifyURL         *string `json:"gotify_url"`
	GotifyToken       *string `json:"gotify_token"`
	DiscordWebhookURL *string `json:"discord_webhook_url"`
	UpdatedAt         int64   `json:"updated_at"`
}

func newNotificationSettingsResponse(
	s db.GlobalNotification,
) notificationSettingsResponse {
	return notificationSettingsResponse{
		ID:                s.ID,
		SlackWebhookURL:   nullStringPtr(s.SlackWebhookUrl),
		NotifyEmails:      nullStringPtr(s.NotifyEmails),
		GotifyURL:         nullStringPtr(s.GotifyUrl),
		GotifyToken:       nullStringPtr(s.GotifyToken),
		DiscordWebhookURL: nullStringPtr(s.DiscordWebhookUrl),
		UpdatedAt:         s.UpdatedAt,
	}
}

// UpdateNotificationSettings updates global notification settings.
// swagger:route PUT /admin/notifications/settings admin updateNotificationSettings
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
//	  200: body:GlobalNotification
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) UpdateNotificationSettings(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req struct {
		SlackWebhookURL   string `json:"slack_webhook_url"`
		NotifyEmails      string `json:"notify_emails"`
		GotifyURL         string `json:"gotify_url"`
		GotifyToken       string `json:"gotify_token"`
		DiscordWebhookURL string `json:"discord_webhook_url"`
	}
	if !readJSONBool(w, r, &req) {
		return
	}

	settings, err := h.repo.Queries.UpdateGlobalNotifications(
		r.Context(),
		db.UpdateGlobalNotificationsParams{
			SlackWebhookUrl: sql.NullString{
				String: req.SlackWebhookURL,
				Valid:  req.SlackWebhookURL != "",
			},
			NotifyEmails: sql.NullString{
				String: req.NotifyEmails,
				Valid:  req.NotifyEmails != "",
			},
			GotifyUrl: sql.NullString{
				String: req.GotifyURL,
				Valid:  req.GotifyURL != "",
			},
			GotifyToken: sql.NullString{
				String: req.GotifyToken,
				Valid:  req.GotifyToken != "",
			},
			DiscordWebhookUrl: sql.NullString{
				String: req.DiscordWebhookURL,
				Valid:  req.DiscordWebhookURL != "",
			},
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
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
//	Schemes: http, https
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
	users, err := countRows(r.Context(), h.repo.DB, "users")
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projects, err := countRows(r.Context(), h.repo.DB, "projects")
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deployments, err := countRows(r.Context(), h.repo.DB, "deployments")
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

func countRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	row := db.QueryRowContext(
		ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s", table),
	)
	var count int64
	err := row.Scan(&count)
	return count, err
}

// swagger:route POST /admin/maintenance admin maintenance
//
// Run maintenance tasks.
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:EmptyResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) Maintenance(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// swagger:route GET /admin/db-tables admin dbTables
//
// List database tables.
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
//	  200: body:DbTableListResponse
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  500: body:ServerError
func (h *AdminHandler) DbTables(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repo.DB.QueryContext(
		r.Context(),
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	RespondJSON(w, http.StatusOK, tables)
}
