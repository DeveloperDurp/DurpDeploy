package auth

import (
	"encoding/json"
	"net/http"
)

// viewerWriteBlockMessage is the single source of truth for the
// "viewer tried to write" error. Surfaced as a toast on HTMX and as
// the heading on the 403 page for non-HTMX form submits.
const viewerWriteBlockMessage = "Viewers cannot perform write operations"

// CSRFMiddleware validates POST/PUT/DELETE/PATCH requests carry the
// correct CSRF token for the active session. It also rejects "viewer"
// role users from any state-changing request — viewers are read-only
// in P0.
//
// Exempt paths: /login (no session yet), /logout (CSRF-exempt by
// decision — the user has a cookie, the handler just clears it).
//
// ponytail: viewer role gate lives here so it fires on every write
// regardless of route. P1 replaces this with per-project authorization.
func CSRFMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost,
				http.MethodPut,
				http.MethodDelete,
				http.MethodPatch:
			default:
				next.ServeHTTP(w, r)
				return
			}

			path := r.URL.Path
			if path == "/login" || path == "/logout" {
				next.ServeHTTP(w, r)
				return
			}

			sess := SessionFromContext(r.Context())
			if sess == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Role gate: viewers cannot perform write operations.
			if RoleFromContext(r.Context()) == "viewer" {
				blockViewerWrite(w, r)
				return
			}

			token := r.PostFormValue("csrf_token")
			if token == "" {
				token = r.Header.Get("X-CSRF-Token")
			}

			if token != sess.CsrfToken {
				http.Error(w, "Invalid CSRF token", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// blockViewerWrite responds to a viewer's write attempt. Two paths:
//   - HTMX: fire a toast via HX-Trigger (the makeToast event handler
//     in static/js/app.js shows it as the same red toast the rest of
//     the app uses). Return 200 so HTMX doesn't surface the failure
//     as an error and the page stays where the user clicked.
//   - non-HTMX: the browser navigates to a fresh 403 page. Render
//     a minimal styled page (no templ dependency — the auth package
//     can't import views/pages without a cycle) with a back link and
//     a script that fires the same toast on load so the user gets
//     the same affordance either way.
//
// ponytail: inlined JSON marshal instead of importing the
// handler.SetToastError helper. The shape is stable ({"level":
// "danger"|"warning"|...,"message": "..."}) and documented in
// static/js/app.js; pulling in handler.toast just to reuse a 5-line
// helper would create a cycle.
func blockViewerWrite(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		event := map[string]map[string]string{
			"makeToast": {
				"level":   "danger",
				"message": viewerWriteBlockMessage,
			},
		}
		data, _ := json.Marshal(event)
		w.Header().Set("HX-Trigger", string(data))
		w.WriteHeader(http.StatusOK)
		return
	}
	// Non-HTMX: render a self-contained 403 page. The back link uses
	// history.back() so the user returns to whatever page they were
	// on. The inline toast on load is best-effort — the toast system
	// lives in the protected-route pages, not the bare auth page, so
	// most viewers will never hit this path (the buttons that would
	// have triggered it are hidden by views/pages.CanWrite).
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(viewerForbiddenHTML))
}

const viewerForbiddenHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Forbidden</title>
<style>
body { font-family: system-ui, sans-serif; background: #1e1e2e; color: #cdd6f4; margin: 0; padding: 4rem 1rem; display: flex; justify-content: center; }
.card { max-width: 32rem; background: #313244; border-radius: 0.5rem; padding: 2rem; }
h1 { margin: 0 0 1rem 0; font-size: 1.25rem; color: #f38ba8; }
p { margin: 0 0 1.5rem 0; line-height: 1.5; }
a { color: #89b4fa; text-decoration: none; }
a:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="card">
<h1>Forbidden</h1>
<p>Viewers cannot perform write operations. If you need to make changes, ask an admin to change your role on the <a href="/admin/users">Users</a> page.</p>
<a href="javascript:history.back()">Go back</a>
</div>
</body>
</html>
`

// RenderUnauthorized writes a self-contained, styled 403 page for
// requests blocked by an authorization check (e.g. non-members hitting
// a project they don't belong to). No templ dependency — the auth
// package can't import views/pages without a cycle — so this mirrors
// viewerForbiddenHTML's inline-HTML approach.
func RenderUnauthorized(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		event := map[string]map[string]string{
			"makeToast": {
				"level":   "danger",
				"message": unauthorizedMessage,
			},
		}
		data, _ := json.Marshal(event)
		w.Header().Set("HX-Trigger", string(data))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(unauthorizedHTML))
}

const unauthorizedMessage = "You don't have access to this"

const unauthorizedHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Unauthorized</title>
<style>
body { font-family: system-ui, sans-serif; background: #1e1e2e; color: #cdd6f4; margin: 0; padding: 4rem 1rem; display: flex; justify-content: center; }
.card { max-width: 32rem; background: #313244; border-radius: 0.5rem; padding: 2rem; }
h1 { margin: 0 0 1rem 0; font-size: 1.25rem; color: #f38ba8; }
p { margin: 0 0 1.5rem 0; line-height: 1.5; }
a { color: #89b4fa; text-decoration: none; }
a:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="card">
<h1>Unauthorized</h1>
<p>You don't have access to this. If you believe this is a mistake, contact an admin.</p>
<a href="/projects">Back to projects</a>
</div>
</body>
</html>
`
