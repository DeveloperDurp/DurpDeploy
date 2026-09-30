package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var ErrReleaseInUse = errors.New(
	"release has deployments or schedules; remove those first",
)

// DeleteRelease preserves deployment history and scheduled deployments.
// The guarded delete is atomic so a concurrent deployment cannot be cascaded.
func (r *Repository) DeleteRelease(
	ctx context.Context,
	projectID, releaseID int64,
) error {
	release, err := r.Queries.GetDeploymentRelease(ctx, releaseID)
	if err != nil {
		return err
	}
	if release.ProjectID != projectID {
		return sql.ErrNoRows
	}

	rows, err := r.Queries.DeleteRelease(ctx, db.DeleteReleaseParams{
		ID: releaseID, ProjectID: projectID,
	})
	if err != nil {
		return fmt.Errorf("delete release: %w", err)
	}
	if rows == 0 {
		if _, err := r.Queries.GetDeploymentRelease(
			ctx,
			releaseID,
		); err != nil {
			return err
		}
		return ErrReleaseInUse
	}
	return nil
}
