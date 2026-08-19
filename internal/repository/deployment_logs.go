package repository

import (
	"context"

	"durpdeploy/internal/db"
)

type DeploymentLogCursor struct {
	DeploymentID int64
	AfterID      int64
}

// ForEachDeploymentLogAfterID streams persisted deployment logs after a cursor.
func (r *Repository) ForEachDeploymentLogAfterID(
	ctx context.Context,
	cursor DeploymentLogCursor,
	fn func(db.DeploymentLog) error,
) error {
	rows, err := r.DB.QueryContext(ctx, `
	SELECT id, deployment_id, step_name, line, created_at, sequence
	FROM deployment_logs
	WHERE deployment_id = ? AND id > ?
	ORDER BY sequence ASC, id ASC`, cursor.DeploymentID, cursor.AfterID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var log db.DeploymentLog
		if err := rows.Scan(
			&log.ID,
			&log.DeploymentID,
			&log.StepName,
			&log.Line,
			&log.CreatedAt,
			&log.Sequence,
		); err != nil {
			return err
		}
		if err := fn(log); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ForEachDeploymentLogByDeploymentAsc streams deployment log rows oldest-first.
func (r *Repository) ForEachDeploymentLogByDeploymentAsc(
	ctx context.Context,
	deploymentID int64,
	fn func(db.DeploymentLog) error,
) error {
	return r.ForEachDeploymentLogAfterID(
		ctx,
		DeploymentLogCursor{DeploymentID: deploymentID},
		fn,
	)
}
