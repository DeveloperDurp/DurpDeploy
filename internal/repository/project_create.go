package repository

import (
	"context"

	"durpdeploy/internal/db"
)

// CreateProject saves the project, creator membership, and group together.
// Validate group visibility before creating the project or its membership.
func (r *Repository) CreateProject(
	ctx context.Context,
	params db.CreateProjectParams,
	access ProjectCatalogAccess,
	groupID *int64,
) (project db.Project, err error) {
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			group, err := accessibleProjectGroup(ctx, q, access, groupID)
			if err != nil {
				return err
			}
			project, err = q.CreateProject(ctx, params)
			if err != nil {
				return err
			}
			if access.UserID != 0 {
				if err := q.AddProjectMember(ctx, db.AddProjectMemberParams{
					ProjectID: project.ID,
					UserID:    access.UserID,
					Role:      "admin",
				}); err != nil {
					return err
				}
			}
			if group.Valid {
				project, err = q.SetProjectGroup(ctx, db.SetProjectGroupParams{
					ID: project.ID, GroupID: group,
				})
			}
			return err
		})
	})
	return project, err
}
