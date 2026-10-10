package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/repository"
)

var ErrLifecycleAssignmentForbidden = errors.New(
	"Only a global admin can assign a lifecycle to a project",
)

var ErrLifecycleRemovalForbidden = errors.New(
	"Only a project admin can remove a lifecycle assignment",
)

// Lifecycle assignment grants access to shared values through execution.
// Project admins may retain or remove that grant, but cannot acquire one.
func AuthorizeLifecycleSelection(
	ctx context.Context,
	repo *repository.Repository,
	projectID, lifecycleID int64,
) error {
	user := auth.UserFromContext(ctx)
	if (projectID == 0 && lifecycleID <= 0) ||
		(user != nil && user.Role == "admin") {
		return nil
	}
	if projectID == 0 {
		return ErrLifecycleAssignmentForbidden
	}
	project, err := repo.Queries.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	if project.LifecycleID.Valid && project.LifecycleID.Int64 == lifecycleID {
		return nil
	}
	if lifecycleID <= 0 {
		if !project.LifecycleID.Valid ||
			CanManageProject(ctx, repo, user, projectID) {
			return nil
		}
		return ErrLifecycleRemovalForbidden
	}
	return ErrLifecycleAssignmentForbidden
}

func (h *ProjectHandler) authorizeLifecycleForm(
	w http.ResponseWriter,
	r *http.Request,
	projectID int64,
) bool {
	lifecycleID, _ := strconv.ParseInt(r.FormValue("lifecycle_id"), 10, 64)
	err := AuthorizeLifecycleSelection(
		r.Context(), h.repo, projectID, lifecycleID,
	)
	if err == nil {
		return true
	}
	if errors.Is(err, ErrLifecycleAssignmentForbidden) ||
		errors.Is(err, ErrLifecycleRemovalForbidden) {
		http.Error(w, err.Error(), http.StatusForbidden)
	} else {
		http.Error(
			w,
			"Could not check lifecycle access",
			http.StatusInternalServerError,
		)
	}
	return false
}
