package auth

import "net/http"

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
			case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
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
				http.Error(w, "Viewers cannot perform write operations", http.StatusForbidden)
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
