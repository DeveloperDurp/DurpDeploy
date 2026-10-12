package auth

import (
	"context"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// CanManageProject checks the authenticated user's global or project admin role.
// Viewer write restrictions remain enforced by the HTTP middleware.
func CanManageProject(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
) bool {
	user := UserFromContext(ctx)
	if user == nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	member, err := repo.Queries.GetProjectMember(ctx, db.GetProjectMemberParams{
		ProjectID: projectID,
		UserID:    user.ID,
	})
	return err == nil && member.Role == "admin"
}
