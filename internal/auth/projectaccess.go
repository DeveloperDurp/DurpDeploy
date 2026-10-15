package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// ProjectAccessKey is the context-key type used to store the project
// id injected by RequireProjectAccess. It is exported so tests that
// call project-scoped handlers directly (without going through the
// router) can populate it the same way the middleware would.
//
// ponytail: a string key would be smaller, but Go convention is
// "unexported is the unexported key, exported is for cases where the
// type needs to be nameable by other packages." Production code
// should still go through RequireProjectAccess; this type is only
// here for tests and for the project-scoped handlers in the api
// package that read it via ProjectIDFromContext.
type ProjectAccessKey struct{}

const deploymentNotFoundMessage = "404 page not found"

// ProjectIDFromContext returns the project id injected by
// RequireProjectAccess, or 0 (and false) if absent.
func ProjectIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ProjectAccessKey{}).(int64)
	return id, ok
}

// RequireProjectAccess returns middleware that admits only users who are
// members of the project named by the `{id}` chi route param. Global
// admins bypass membership. Scoped tokens also require a selected project.
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
	return requireProjectAccess(repo, http.StatusForbidden)
}

// RequireProjectAccessNotFound hides inaccessible deletion targets.
func RequireProjectAccessNotFound(
	repo *repository.Repository,
) func(http.Handler) http.Handler {
	return requireProjectAccess(repo, http.StatusNotFound)
}

func requireProjectAccess(
	repo *repository.Repository,
	deniedStatus int,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveProjectAccess(w, r, next, repo, deniedStatus)
		})
	}
}

func serveProjectAccess(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	repo *repository.Repository,
	deniedStatus int,
) {
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

	if !TokenAllowsProject(r.Context(), projectID) {
		RenderRequestError(w, r, deniedStatus, "project access denied")
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

		if !checkProjectMembershipStatus(
			w,
			r,
			repo,
			db.IsProjectMemberParams{
				UserID:    user.ID,
				ProjectID: projectID,
			},
			deniedStatus,
		) {
			return
		}
	}

	setTokenProject(r.Context(), projectID)
	ctx := context.WithValue(r.Context(), ProjectAccessKey{}, projectID)
	next.ServeHTTP(w, r.WithContext(ctx))
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
				RenderRequestError(
					w,
					r,
					http.StatusUnauthorized,
					"Unauthorized",
				)
				return
			}

			idStr := chi.URLParam(r, "id")
			deploymentID, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil || deploymentID <= 0 {
				RenderRequestError(
					w,
					r,
					http.StatusBadRequest,
					"Invalid deployment id",
				)
				return
			}

			// 404 (not 403) on a missing deployment/release hides
			// existence from non-members.
			deployment, err := repo.Queries.GetDeployment(
				r.Context(),
				deploymentID,
			)
			if err != nil {
				RenderRequestError(
					w,
					r,
					http.StatusNotFound,
					deploymentNotFoundMessage,
				)
				return
			}
			if deployment.Kind != "deployment" {
				RenderRequestError(
					w,
					r,
					http.StatusNotFound,
					deploymentNotFoundMessage,
				)
				return
			}
			release, err := repo.Queries.GetRelease(
				r.Context(),
				deployment.ReleaseID,
			)
			if err != nil {
				// Preserve unrestricted admin access if release lookup fails.
				if user.Role == "admin" && ScopedTokenID(r.Context()) == "" {
					next.ServeHTTP(w, r)
					return
				}
				RenderRequestError(
					w,
					r,
					http.StatusNotFound,
					deploymentNotFoundMessage,
				)
				return
			}

			if !TokenAllowsProject(r.Context(), release.ProjectID) {
				RenderRequestError(w, r, http.StatusForbidden,
					"project access denied")
				return
			}
			if user.Role != "admin" &&
				!checkProjectMembership(
					w,
					r,
					repo,
					user.ID,
					release.ProjectID,
				) {
				return
			}
			setTokenProject(r.Context(), release.ProjectID)
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
	return checkProjectMembershipStatus(w, r, repo,
		db.IsProjectMemberParams{UserID: userID, ProjectID: projectID},
		http.StatusForbidden)
}

func checkProjectMembershipStatus(
	w http.ResponseWriter,
	r *http.Request,
	repo *repository.Repository,
	memberParams db.IsProjectMemberParams,
	deniedStatus int,
) bool {
	member, err := repo.Queries.IsProjectMember(
		r.Context(),
		memberParams,
	)
	if err != nil {
		RenderRequestError(w, r, http.StatusInternalServerError,
			"Internal server error")
		return false
	}
	if member != 1 {
		if deniedStatus == http.StatusNotFound {
			RenderRequestError(w, r, http.StatusNotFound, "Runbook not found")
			return false
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			RenderJSONError(w, http.StatusForbidden, unauthorizedMessage)
		} else {
			RenderUnauthorized(w, r)
		}
		return false
	}
	return true
}
