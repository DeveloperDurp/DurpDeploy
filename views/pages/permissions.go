package pages

import (
	"context"

	"durpdeploy/internal/auth"
)

// CanWrite checks authoring permission, including the project ceiling.
// Execution controls use CanDeploy. HTTP middleware enforces both checks.
//
// ponytail: explicit `ctx` parameter instead of reading the
// package-level `ctx` that templ injects into component bodies.
// Helper functions in templ files don't have access to that implicit
// `ctx` (it's a local in the component scope), so the parameter is
// the cleanest way to expose this from a non-component Go function.
func CanWrite(ctx context.Context) bool {
	u := auth.UserFromContext(ctx)
	return u == nil || auth.CanAuthor(ctx)
}

func CanDeploy(ctx context.Context) bool {
	return auth.UserFromContext(ctx) == nil || auth.CanDeploy(ctx)
}

func CanManageAgents(ctx context.Context) bool {
	u := auth.UserFromContext(ctx)
	return u != nil && u.Role == "admin"
}

func CanManageLifecycleVariables(ctx context.Context) bool {
	u := auth.UserFromContext(ctx)
	return u != nil && u.Role == "admin"
}
