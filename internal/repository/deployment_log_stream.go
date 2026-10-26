package repository

import (
	"context"
	"errors"

	"durpdeploy/internal/db"
)

// ForEachDeploymentLogByDeploymentAsc streams deployment log rows
// oldest-first in configured batches, and only rows that existed when
// iteration started. Rows are closed before fn is called for a batch so a
// slow export client cannot pin a database connection.
//
// The WHERE clause bounds iteration to a max(id) watermark taken up
// front: without it, logs appended by an active deployment would keep
// extending the export (and SSE historical replay) indefinitely. Pages
// are plain OFFSET reads — the watermark freezes the row set and the
// trailing l.id tiebreak makes the order total, so pages stay stable.
//
// The select must stay outermost (no subquery/CTE wrap): the SQL Server
// LIMIT rewriter attaches TOP to the first SELECT in the statement.
//
// ponytail: each page rescans this deployment's logs because the sort
// keys are computed, not indexed — fine at realistic log volumes; if
// exports ever get huge, stream from one materialized ordered-id pass.
func (r *Repository) ForEachDeploymentLogByDeploymentAsc(
	ctx context.Context,
	deploymentID int64,
	fn func(db.DeploymentLog) error,
) error {
	batchSize := int(r.ResourceLimits().Logs.ExportBatch)
	var watermark int64
	if err := r.DB.QueryRowContext(ctx, `
SELECT COALESCE(MAX(id), 0)
FROM deployment_logs
WHERE deployment_id = ?`, deploymentID).Scan(&watermark); err != nil {
		return err
	}
	var offset int64
	for {
		rows, err := r.DB.QueryContext(ctx, `
SELECT l.id, l.deployment_id, l.step_name, l.line, l.created_at,
    l.step_index, l.step_state
FROM deployment_logs l
LEFT JOIN deployment_log_scopes s ON s.log_id = l.id
WHERE l.deployment_id = ?
    AND l.id <= ?
ORDER BY CASE
    WHEN s.step_index IS NULL AND s.attempt IS NULL THEN 0 ELSE 1
END,
CASE
    WHEN s.step_index IS NULL AND s.attempt IS NULL
        THEN COALESCE(s.sequence, -1) ELSE -1
END,
l.created_at ASC, l.id ASC
LIMIT ? OFFSET ?`, deploymentID, watermark, batchSize, offset)
		if err != nil {
			return err
		}

		logs := make([]db.DeploymentLog, 0, batchSize)
		for rows.Next() {
			var log db.DeploymentLog
			if err := rows.Scan(
				&log.ID,
				&log.DeploymentID,
				&log.StepName,
				&log.Line,
				&log.CreatedAt,
				&log.StepIndex,
				&log.StepState,
			); err != nil {
				return errors.Join(err, rows.Close())
			}
			logs = append(logs, log)
		}
		if err := rows.Err(); err != nil {
			return errors.Join(err, rows.Close())
		}
		if err := rows.Close(); err != nil {
			return err
		}

		for _, log := range logs {
			if err := fn(log); err != nil {
				return err
			}
		}
		offset += int64(len(logs))
		if len(logs) < batchSize {
			return nil
		}
	}
}
