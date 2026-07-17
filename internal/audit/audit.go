package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// Entry is the audit record a caller wants to persist. Callers that
// cannot be reached by the middleware (public routes like /login and
// /logout) build one of these and call Record directly.
type Entry struct {
	UserID     sql.NullInt64
	Action     string
	EntityType string
	EntityID   sql.NullInt64
	Details    string // raw JSON string; empty means NULL
}

// Record inserts an audit_log row. It NEVER returns an error that the
// caller should act on — audit is observability, not a gate. On insert
// failure it logs a warning and returns nil so the request proceeds.
//
// ponytail: swallow-and-log instead of error-return. The plan is
// explicit: "do NOT block the request if the audit insert fails". If
// audit volume ever needs backpressure, add a buffered queue here.
func Record(ctx context.Context, repo *repository.Repository, e Entry) {
	var details sql.NullString
	if e.Details != "" {
		details = sql.NullString{String: e.Details, Valid: true}
	}
	if _, err := repo.Queries.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:     e.UserID,
		Action:     e.Action,
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Details:    details,
	}); err != nil {
		slog.Warn("audit insert failed", "action", e.Action, "err", err)
	}
}

// actionMap maps "METHOD /route/pattern" → audit action. Patterns use
// chi's {param} form so they match the resolved RoutePattern, not the
// raw URL. Routes not in this map fall back to the method+segment
// heuristic below.
//
// ponytail: explicit map over reflection. Covers every state-changing
// route registered in server.go. New routes need one map entry; the
// fallback heuristic catches anything missed with a lossy name.
var actionMap = map[string]string{
	"POST /login":                                      "login",
	"POST /logout":                                     "logout",
	"POST /projects":                                   "create_project",
	"PUT /projects/{id}":                               "update_project",
	"DELETE /projects/{id}":                            "delete_project",
	"POST /environments":                               "create_environment",
	"PUT /environments/{id}":                           "update_environment",
	"DELETE /environments/{id}":                        "delete_environment",
	"POST /projects/{id}/steps":                        "create_step",
	"PUT /projects/{id}/steps/{stepId}":                "update_step",
	"DELETE /projects/{id}/steps/{stepId}":             "delete_step",
	"POST /projects/{id}/releases":                     "create_release",
	"POST /projects/{id}/releases/{releaseId}/refresh": "refresh_release",
	"POST /projects/{id}/variables":                    "create_variable",
	"PUT /projects/{id}/variables/{varId}":             "update_variable",
	"DELETE /projects/{id}/variables/{varId}":          "delete_variable",
	"POST /projects/{id}/deploy":                       "create_deployment",
	"POST /deployments/{id}/cancel":                    "cancel_deployment",
	"POST /deployments/{id}/approve":                   "approve_deployment",
	"POST /deployments/{id}/redeploy":                  "redeploy_deployment",
	"POST /lifecycles":                                 "create_lifecycle",
	"POST /lifecycles/{id}/stages":                     "create_lifecycle_stage",
	"POST /projects/{id}/schedules":                    "create_schedule",
	"PUT /projects/{id}/schedules/{schedId}":           "update_schedule",
	"DELETE /projects/{id}/schedules/{schedId}":        "delete_schedule",
	"POST /projects/{id}/schedules/{schedId}/toggle":   "toggle_schedule",
	"POST /projects/{id}/members":                      "add_project_member",
	"DELETE /projects/{id}/members/{userId}":           "remove_project_member",
	"POST /templates":                                  "create_template",
	"PUT /templates/{id}":                              "update_template",
	"DELETE /templates/{id}":                           "delete_template",
	"POST /admin/users":                                "create_user",
	"PUT /admin/users/{id}":                            "update_user",
	"DELETE /admin/users/{id}":                         "delete_user",
}

// entityIDRe captures the first numeric path segment, e.g. /projects/42
// → 42. Used to populate entity_id when the route references one.
var entityIDRe = regexp.MustCompile(`^/\w+/(\d+)`)

// methodVerb maps an HTTP method to the action verb used by the
// fallback heuristic when a route is not in actionMap.
var methodVerb = map[string]string{
	http.MethodPost:   "create",
	http.MethodPut:    "update",
	http.MethodPatch:  "update",
	http.MethodDelete: "delete",
}

// Middleware wraps a protected route group and records an audit_log
// entry for every successful state-changing request (POST/PUT/PATCH/
// DELETE returning 2xx/3xx). It must run AFTER auth.AuthMiddleware
// (so the user is in context) and AFTER auth.CSRFMiddleware (so CSRF
// rejections don't generate audit entries).
//
// ponytail: central middleware over per-handler inserts. The only
// exceptions are /login and /logout which are public and call Record
// directly. Ceiling: action inference is lossy for routes outside
// actionMap — the fallback heuristic names them verb_<singularized
// first segment>. Add a map entry when you need the exact name.
func Middleware(repo *repository.Repository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			if !isStateChanging(r.Method) {
				return
			}
			if !successStatus(ww.Status()) {
				return
			}

			action, entityType := deriveAction(r)
			if action == "" {
				return
			}

			var userID sql.NullInt64
			if u := auth.UserFromContext(r.Context()); u != nil {
				userID = sql.NullInt64{Int64: u.ID, Valid: true}
			}

			var entityID sql.NullInt64
			if m := entityIDRe.FindStringSubmatch(r.URL.Path); m != nil {
				if v, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					entityID = sql.NullInt64{Int64: v, Valid: true}
				}
			}

			Record(r.Context(), repo, Entry{
				UserID:     userID,
				Action:     action,
				EntityType: entityType,
				EntityID:   entityID,
				Details:    buildDetails(r, ww.Status()),
			})
		})
	}
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func successStatus(status int) bool {
	return status >= 200 && status < 400
}

// deriveAction returns (action, entity_type). It first tries the
// explicit actionMap keyed by "METHOD <chi RoutePattern>". If the
// pattern is empty or absent, it falls back to a method+segment
// heuristic.
func deriveAction(r *http.Request) (string, string) {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if pattern != "" {
		if action, ok := actionMap[r.Method+" "+pattern]; ok {
			return action, entityTypeFromAction(action)
		}
	}
	return fallbackAction(r)
}

// entityTypeFromAction splits "create_project" → "project", "login" →
// "user". Single-word actions (login, logout) are user-scoped.
func entityTypeFromAction(action string) string {
	if idx := strings.Index(action, "_"); idx != -1 {
		return action[idx+1:]
	}
	return "user"
}

// fallbackAction derives a lossy action from method + first path
// segment: POST /projects/... → "create_project". The entity_type is
// the singular form of the first segment.
func fallbackAction(r *http.Request) (string, string) {
	verb, ok := methodVerb[r.Method]
	if !ok {
		return "", ""
	}
	seg := firstPathSegment(r.URL.Path)
	if seg == "" {
		return "", ""
	}
	entity := singularize(seg)
	return verb + "_" + entity, entity
}

func firstPathSegment(path string) string {
	s := strings.Trim(path, "/")
	if s == "" {
		return ""
	}
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}
	return s
}

// singularize strips a trailing 's' from the segment. Good enough for
// the registered routes (projects, environments, deployments,
// lifecycles, templates). "variables" → "variable", "steps" → "step".
//
// ponytail: naive plural strip. Replace with a proper inflector if
// routes ever use irregular plurals.
func singularize(seg string) string {
	if strings.HasSuffix(seg, "s") && len(seg) > 1 {
		return seg[:len(seg)-1]
	}
	return seg
}

// buildDetails marshals a small JSON object with request metadata plus
// the entity "name" form field when present. The middleware runs after
// the handler, so r.Form is already populated and r.FormValue("name")
// returns the parsed value.
//
// ponytail: log only the "name" field, never the full form. "name" is
// the human label for projects/environments/lifecycles/templates/steps
// — never secret. /login (email/password) is outside the middleware
// group, and variables use "key"/"value" (not "name"), so secret
// variable values and passwords are never logged here. If a future
// route puts a secret in a field literally named "name", gate it in
// actionMap instead.
func buildDetails(r *http.Request, status int) string {
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	details := map[string]any{
		"ip":         ip,
		"user_agent": r.UserAgent(),
		"status":     status,
		"method":     r.Method,
		"path":       r.URL.Path,
	}
	if name := r.FormValue("name"); name != "" {
		details["name"] = name
	}
	b, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(b)
}
