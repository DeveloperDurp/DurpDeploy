package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var ErrReleaseHasActiveDeployment = errors.New(
	"release has active or unconfirmed deployments",
)

// DeleteRelease removes schedules and completed deployment history atomically.
func (r *Repository) DeleteRelease(
	ctx context.Context,
	projectID, releaseID int64,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if err := lockRelease(ctx, q, releaseID); err != nil {
				return err
			}
			release, err := q.GetDeploymentRelease(ctx, releaseID)
			if err != nil {
				return err
			}
			if release.ProjectID != projectID {
				return sql.ErrNoRows
			}
			active, err := q.HasActiveReleaseDeployment(ctx, releaseID)
			if err != nil {
				return err
			}
			if active != 0 {
				return ErrReleaseHasActiveDeployment
			}
			deployments, err := q.ListDeploymentsByRelease(ctx, releaseID)
			if err != nil {
				return err
			}
			for _, deployment := range deployments {
				if err := deleteDeploymentHistory(
					ctx,
					q,
					deployment.ID,
				); err != nil {
					return err
				}
			}
			if err := q.ClearArtifactSourceReleaseByRelease(
				ctx,
				sql.NullInt64{Int64: releaseID, Valid: true},
			); err != nil {
				return err
			}
			if err := q.DeleteReleaseArtifact(ctx, releaseID); err != nil {
				return err
			}
			return q.DeleteRelease(ctx, db.DeleteReleaseParams{
				ID: releaseID, ProjectID: projectID,
			})
		})
	})
}

func lockRelease(ctx context.Context, q *db.Queries, releaseID int64) error {
	release, err := q.GetRelease(ctx, releaseID)
	if err != nil {
		return err
	}
	projectRows, err := q.LockProject(ctx, release.ProjectID)
	if err != nil {
		return err
	}
	if projectRows == 0 {
		return sql.ErrNoRows
	}
	rows, err := q.LockRelease(ctx, releaseID)
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
