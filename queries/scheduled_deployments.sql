-- name: CreateScheduledDeployment :one
INSERT INTO scheduled_deployments (project_id, release_id, environment_id, cron, next_run_at, enabled, last_fired_at, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetScheduledDeployment :one
SELECT * FROM scheduled_deployments WHERE id = ?;

-- name: ListScheduledDeploymentsByProject :many
SELECT * FROM scheduled_deployments WHERE project_id = ? ORDER BY created_at DESC;

-- name: ListDueScheduledDeployments :many
SELECT * FROM scheduled_deployments WHERE next_run_at <= ? AND enabled = 1 ORDER BY next_run_at ASC;

-- name: UpdateScheduledDeployment :one
UPDATE scheduled_deployments SET project_id = ?, release_id = ?, environment_id = ?, cron = ?, next_run_at = ?, enabled = ?, last_fired_at = ?, note = ? WHERE id = ? RETURNING *;

-- name: UpdateScheduledDeploymentNextRun :exec
UPDATE scheduled_deployments SET next_run_at = ?, updated_at = unixepoch() WHERE id = ?;

-- name: DeleteScheduledDeployment :exec
DELETE FROM scheduled_deployments WHERE id = ?;

-- name: ToggleScheduledDeploymentEnabled :one
UPDATE scheduled_deployments SET enabled = CASE WHEN enabled = 1 THEN 0 ELSE 1 END, updated_at = unixepoch() WHERE id = ? RETURNING *;
