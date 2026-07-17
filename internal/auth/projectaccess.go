package auth

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// projectAccessKey is an unexported context-key type so callers can't
// collide with string keys. Handlers downstream read the resolved
// project id via ProjectIDFromContext instead of re-parsing the chi
// URL param.
type projectAccessKey struct{}

// ProjectIDFromContext returns the project id injected by
// RequireProjectAccess, or 0 (and false) if absent.
func ProjectIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(projectAccessKey{}).(int64)
	return id, ok
}

// RequireProjectAccess returns middleware that admits only users who are
// members of the project named by the `{id}` chi route param. Global
// admins bypass the membership check entirely.
//
// Ordering: this middleware runs AFTER auth.AuthMiddleware (which injects
// the user) and auth.CSRFMiddleware (which already blocks "viewer" from
// state-changing requests), and BEFORE the handler. It does not re-check
// the global role for writes — that is the CSRF middleware's job.
//
// ponytail: finer-grained per-project role enforcement (e.g. "only a
// per-project admin can edit project settings, not a per-project deployer")
// is a follow-up that belongs in handler-level checks, not here. This
// middleware treats ANY project_members row as authorized; the per-project
// role column is captured for that future use.
func RequireProjectAccess(
	repo *repository.Repository,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
			if user == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Defensive: the route pattern is /projects/{id}, so chi
			// should always populate this. 400 keeps the contract honest
			// if a route is misconfigured.
			idStr := chi.URLParam(r, "id")
			projectID, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil || projectID <= 0 {
				http.Error(w, "Invalid project id", http.StatusBadRequest)
				return
			}

			// Global admin bypasses the membership check, but the
			// project id is still injected into the context so handlers
			// can uniformly use ProjectIDFromContext instead of re-parsing.
			if user.Role != "admin" {
				// 404 (not 403) on a missing project hides the project's
				// existence from non-members.
				if _, err := repo.Queries.GetProject(
					r.Context(),
					projectID,
				); err != nil {
					http.NotFound(w, r)
					return
				}

				if !checkProjectMembership(w, r, repo, user.ID, projectID) {
					return
				}
			}

			ctx := context.WithValue(r.Context(), projectAccessKey{}, projectID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireDeploymentProjectAccess is like RequireProjectAccess, but for
// routes where the `{id}` chi route param is a deployment id rather than
// a project id (e.g. /deployments/{id}, /deployments/{id}/logs/stream).
// The project is resolved via deployment -> release -> project so the
// same membership check applies to deployment detail and log routes.
func RequireDeploymentProjectAccess(
	repo *repository.Repository,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
			if user == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Global admin bypasses the membership check.
			if user.Role == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			idStr := chi.URLParam(r, "id")
			deploymentID, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil || deploymentID <= 0 {
				http.Error(w, "Invalid deployment id", http.StatusBadRequest)
				return
			}

			// 404 (not 403) on a missing deployment/release hides
			// existence from non-members.
			deployment, err := repo.Queries.GetDeployment(
				r.Context(),
				deploymentID,
			)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			release, err := repo.Queries.GetRelease(
				r.Context(),
				deployment.ReleaseID,
			)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			if !checkProjectMembership(w, r, repo, user.ID, release.ProjectID) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// checkProjectMembership writes an error response and returns false if
// the user is not a member of the project; returns true otherwise.
func checkProjectMembership(
	w http.ResponseWriter,
	r *http.Request,
	repo *repository.Repository,
	userID, projectID int64,
) bool {
	member, err := repo.Queries.IsProjectMember(
		r.Context(),
		db.IsProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
		},
	)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return false
	}
	if member != 1 {
		RenderUnauthorized(w, r)
		return false
	}
	return true
}
