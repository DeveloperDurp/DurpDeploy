package pages

import (
	"context"

	"durpdeploy/internal/auth"
)

// CanWrite returns false if the current user is a viewer. Viewers
// are read-only by P0 design — every write path is blocked at the
// CSRF middleware, so the UI just needs to not show the button in
// the first place. Use as `if CanWrite(ctx) { ... }` in templ
// components to hide write affordances (New / Edit / Delete buttons,
// form submit buttons, etc.).
//
// ponytail: explicit `ctx` parameter instead of reading the
// package-level `ctx` that templ injects into component bodies.
// Helper functions in templ files don't have access to that implicit
// `ctx` (it's a local in the component scope), so the parameter is
// the cleanest way to expose this from a non-component Go function.
func CanWrite(ctx context.Context) bool {
	u := auth.UserFromContext(ctx)
	return u == nil || u.Role != "viewer"
}
