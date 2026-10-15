package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/roles"
)

type projectRoleKey struct{}

// ProjectRoleFromContext applies membership after the owner and token ceiling.
func ProjectRoleFromContext(ctx context.Context) string {
	role := RoleFromContext(ctx)
	if member, ok := ctx.Value(projectRoleKey{}).(string); ok &&
		roles.Rank(member) < roles.Rank(role) {
		return member
	}
	return role
}

func CanAuthor(ctx context.Context) bool {
	return roles.AtLeast(ProjectRoleFromContext(ctx), "maintainer")
}

func CanDeploy(ctx context.Context) bool {
	return roles.AtLeast(ProjectRoleFromContext(ctx), "deployer")
}

// deploymentWrite admits only the existing-release execution endpoints.
// Runbooks, rollback, approvals, and every configuration write require authoring.
func deploymentWrite(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "" {
		return false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return false
	}
	if parts[1] == "projects" {
		return r.URL.Path == path && parts[3] == "deploy" ||
			r.URL.Path != path && parts[3] == "deployments"
	}
	return parts[1] == "deployments" &&
		(parts[3] == "cancel" || parts[3] == "redeploy" ||
			r.URL.Path != path && parts[3] == "retry")
}

func projectWriteAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return CanAuthor(r.Context()) ||
			CanDeploy(r.Context()) && deploymentWrite(r)
	default:
		return true
	}
}

// RejectRestrictedWrite preserves Viewer responses and denies Deployer authoring.
func RejectRestrictedWrite(w http.ResponseWriter, r *http.Request) bool {
	// Public login and MFA forms also use the web body limiter.
	// Their handlers authenticate the pending login without a session user.
	if UserFromContext(r.Context()) == nil {
		return false
	}
	if RejectViewerWrite(w, r) {
		return true
	}
	if viewerSecurityWrite(r.URL.Path) ||
		roles.AtLeast(RoleFromContext(r.Context()), "maintainer") ||
		RoleFromContext(r.Context()) == "deployer" && deploymentWrite(r) {
		return false
	}
	RenderUnauthorized(w, r)
	return true
}
