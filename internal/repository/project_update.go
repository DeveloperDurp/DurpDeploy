package repository

import (
	"context"

	"durpdeploy/internal/db"
)

// UpdateProject commits metadata and the authorized lifecycle change together.
// applyLifecycle must use the supplied transaction-bound repository queries.
func (r *Repository) UpdateProject(
	ctx context.Context,
	params db.UpdateProjectParams,
	applyLifecycle func(*Repository) error,
) (project db.Project, err error) {
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.UpdateProject(ctx, params); err != nil {
				return err
			}
			txRepo := *r
			txRepo.Queries = q
			if err := applyLifecycle(&txRepo); err != nil {
				return err
			}
			project, err = q.GetProject(ctx, params.ID)
			return err
		})
	})
	return project, err
}
