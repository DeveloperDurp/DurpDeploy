package handler

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var ErrReleaseSnapshotLocked = errors.New(
	"release has been used by a deployment; create a new release instead of refreshing",
)

func lockRefreshableRelease(
	ctx context.Context, q *db.Queries, releaseID int64,
) error {
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
	changed, err := q.LockUnusedReleaseSnapshot(ctx, releaseID)
	if err != nil {
		return err
	}
	if changed == 0 {
		release, err := q.GetRelease(ctx, releaseID)
		if err != nil {
			return err
		}
		if release.SnapshotLocked != 0 {
			return ErrReleaseSnapshotLocked
		}
		return sql.ErrNoRows
	}
	return nil
}
