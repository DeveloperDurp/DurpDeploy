package handler

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

func lockReleaseForRefresh(
	ctx context.Context, q *db.Queries, release db.Release,
) error {
	// Acquire the write lock before a SQLite read snapshot can block its upgrade.
	projectRows, err := q.LockProject(ctx, release.ProjectID)
	if err != nil {
		return err
	}
	if projectRows == 0 {
		return sql.ErrNoRows
	}
	changed, err := q.LockRelease(ctx, release.ID)
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}
