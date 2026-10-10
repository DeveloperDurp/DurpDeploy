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

// Lifecycle assignment grants access to shared values through execution.
// Project admins may retain or remove that grant, but cannot acquire one.
func AuthorizeLifecycleSelection(
	ctx context.Context,
	repo *repository.Repository,
	projectID, lifecycleID int64,
) error {
	user := auth.UserFromContext(ctx)
	if lifecycleID <= 0 || (user != nil && user.Role == "admin") {
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
	if errors.Is(err, ErrLifecycleAssignmentForbidden) {
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
