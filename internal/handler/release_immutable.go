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
	if _, err := q.LockProject(ctx, release.ProjectID); err != nil {
		return err
	}
	changed, err := q.LockUnusedReleaseSnapshot(ctx, releaseID)
	if err != nil {
		return err
	}
	if changed == 0 {
		if _, err := q.GetRelease(
			ctx,
			releaseID,
		); errors.Is(
			err,
			sql.ErrNoRows,
		) {
			return err
		}
		return ErrReleaseSnapshotLocked
	}
	return nil
}
