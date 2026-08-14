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
type requestStateKey struct{}

type requestState struct {
	suppress   bool
	overridden *actionOverride
}

type actionOverride struct {
	action     string
	entityType string
	entityID   sql.NullInt64
	reason     string
}

func Suppress(r *http.Request) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.suppress = true
	}
}

func SetMFAAdminReset(r *http.Request, targetID int64, reason string) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.overridden = &actionOverride{
			action:     "mfa_admin_reset",
			entityType: "admin_reset",
			entityID:   sql.NullInt64{Int64: targetID, Valid: true},
			reason:     reason,
		}
	}
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
			state := &requestState{}
			r = r.WithContext(
				context.WithValue(r.Context(), requestStateKey{}, state),
			)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			if state.suppress {
				return
			}
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
			if routeID := chi.URLParam(r, "id"); routeID != "" {
				if v, err := strconv.ParseInt(routeID, 10, 64); err == nil {
					entityID = sql.NullInt64{Int64: v, Valid: true}
				}
			} else if m := entityIDRe.FindStringSubmatch(r.URL.Path); m != nil {
				if v, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					entityID = sql.NullInt64{Int64: v, Valid: true}
				}
			}
			reason := ""
			if state.overridden != nil {
				action = state.overridden.action
				entityType = state.overridden.entityType
				entityID = state.overridden.entityID
				reason = state.overridden.reason
			}

			Record(r.Context(), repo, Entry{
				UserID:     userID,
				Action:     action,
				EntityType: entityType,
				EntityID:   entityID,
				Details:    buildDetails(r, ww.Status(), action, reason),
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
func buildDetails(
	r *http.Request,
	status int,
	action string,
	canonicalReason string,
) string {
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
	if action == "mfa_admin_reset" {
		reason := canonicalReason
		if reason == "" {
			reason = r.PostFormValue("reason")
		}
		details["reason"] = reason
	}
	b, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(b)
}
