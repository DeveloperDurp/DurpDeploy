package auth

import (
	"context"
	"net/http"
)

// RoleFromContext returns the authenticated user's role, or "" if no
// user is present in the context.
func RoleFromContext(ctx context.Context) string {
	if u := UserFromContext(ctx); u != nil {
		return u.Role
	}
	return ""
}

// RequireRole returns middleware that admits only the listed roles.
// A missing user yields 401; a role not in the allow-list yields 403.
//
// ponytail: P0 simple global role gate. P1 adds per-project authorization
// (project_members table) which replaces this coarse check.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			if _, ok := allowed[u.Role]; !ok {
				RenderUnauthorized(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
