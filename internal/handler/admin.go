package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

// AdminHandler serves the admin-only audit log viewer.
type AdminHandler struct {
	repo *repository.Repository
}

func NewAdminHandler(repo *repository.Repository) *AdminHandler {
	return &AdminHandler{repo: repo}
}

// ListAudit renders /admin/audit: the last 200 audit_log entries with
// optional filters by user_id, action, and entity_type. Admin role
// required — enforced by the RequireRole("admin") middleware mounted
// on the /admin/* sub-group in server.go.
func (h *AdminHandler) ListAudit(w http.ResponseWriter, r *http.Request) {
	params := db.ListAuditLogsFilteredParams{
		PageLimit: 200,
	}

	if v := r.URL.Query().Get("user_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			params.FUserID = sql.NullInt64{Int64: id, Valid: true}
		}
	}
	if v := r.URL.Query().Get("action"); v != "" {
		params.FAction = sql.NullString{String: v, Valid: true}
	}
	if v := r.URL.Query().Get("entity_type"); v != "" {
		params.FEntityType = sql.NullString{String: v, Valid: true}
	}

	entries, err := h.repo.Queries.ListAuditLogsFiltered(r.Context(), params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user := auth.UserFromContext(r.Context())
	if err := pages.AuditLogPage(entries, r.URL.Path, user).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ListNotifications renders /admin/notifications: the last 100 events
// published to the internal EventBus, along with each notifier's delivery
// status (ok/skipped/failed), so admins can observe that Slack/email
// notifications actually fire. Admin role required (see ListAudit above).
func (h *AdminHandler) ListNotifications(
	w http.ResponseWriter,
	r *http.Request,
) {
	entries, err := h.repo.Queries.ListNotificationEvents(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user := auth.UserFromContext(r.Context())
	if err := pages.NotificationsPage(entries, r.URL.Path, user).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
