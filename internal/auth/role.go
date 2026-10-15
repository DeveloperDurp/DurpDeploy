package auth

import (
	"context"
	"net/http"
	"strings"
)

// RoleFromContext intersects the owner's role with the token's ceiling.
// Browser sessions and legacy tokens retain the owner's role.
func RoleFromContext(ctx context.Context) string {
	if u := UserFromContext(ctx); u != nil {
		if token := TokenFromContext(ctx); token != nil &&
			!tokenRoleAllows(ctx, u.Role) {
			return token.Role
		}
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
			if _, ok := allowed[RoleFromContext(r.Context())]; !ok {
				if strings.HasPrefix(r.URL.Path, "/api/v1/") {
					RenderJSONError(w, http.StatusForbidden,
						unauthorizedMessage)
				} else {
					RenderUnauthorized(w, r)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
