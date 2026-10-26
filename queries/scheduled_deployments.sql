-- name: CreateScheduledDeployment :one
INSERT INTO scheduled_deployments (project_id, release_id, environment_id, cron, next_run_at, enabled, last_fired_at, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetScheduledDeployment :one
SELECT * FROM scheduled_deployments WHERE id = ?;

-- name: ListScheduledDeploymentsByProject :many
SELECT * FROM scheduled_deployments WHERE project_id = ? ORDER BY created_at DESC;

-- name: ListScheduledDeploymentsByProjectPaginated :many
SELECT * FROM scheduled_deployments WHERE project_id = ? ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListScheduledDeploymentDetails :many
SELECT sqlc.embed(scheduled_deployments), releases.version AS release_version,
       environments.name AS environment_name
FROM scheduled_deployments
JOIN releases ON releases.id = scheduled_deployments.release_id
JOIN environments ON environments.id = scheduled_deployments.environment_id
WHERE scheduled_deployments.project_id = ?
ORDER BY scheduled_deployments.created_at DESC, scheduled_deployments.id DESC
LIMIT ? OFFSET ?;

-- name: CountScheduledDeploymentsByProject :one
SELECT COUNT(*) FROM scheduled_deployments WHERE project_id = ?;

-- name: ListDueScheduledDeployments :many
SELECT * FROM scheduled_deployments WHERE next_run_at <= ? AND enabled = 1 ORDER BY next_run_at ASC;

-- name: UpdateScheduledDeployment :one
UPDATE scheduled_deployments SET project_id = ?, release_id = ?, environment_id = ?, cron = ?, next_run_at = ?, enabled = ?, last_fired_at = ?, note = ?, last_error = '' WHERE id = ? RETURNING *;

-- name: UpdateScheduledDeploymentNextRun :exec
UPDATE scheduled_deployments SET next_run_at = ?, updated_at = unixepoch() WHERE id = ?;

-- name: AdvanceScheduledDeploymentOccurrence :execrows
UPDATE scheduled_deployments SET next_run_at = sqlc.arg(next_run_at), last_fired_at = sqlc.narg(fired_at), enabled = sqlc.arg(enabled), last_error = sqlc.arg(last_error), updated_at = unixepoch()
WHERE id = sqlc.arg(id) AND enabled = 1 AND next_run_at = sqlc.arg(observed_run_at)
AND project_id = sqlc.arg(project_id) AND release_id = sqlc.arg(release_id) AND environment_id = sqlc.arg(environment_id)
AND cron = sqlc.arg(cron) AND COALESCE(note, '') = COALESCE(sqlc.narg(note), '');

-- name: DisableScheduledDeploymentWithReason :execrows
UPDATE scheduled_deployments SET enabled = 0, last_error = ?, updated_at = unixepoch()
WHERE id = ? AND enabled = 1 AND next_run_at = ?;

-- name: DeleteScheduledDeployment :exec
DELETE FROM scheduled_deployments WHERE id = ?;

-- name: ToggleScheduledDeploymentEnabled :one
UPDATE scheduled_deployments SET enabled = CASE WHEN enabled = 1 THEN 0 ELSE 1 END,
    last_error = CASE WHEN enabled = 0 THEN '' ELSE last_error END,
    updated_at = unixepoch() WHERE id = ? RETURNING *;
