-- name: CreateRunbook :one
INSERT INTO runbooks (project_id, name, description) VALUES (?, ?, ?) RETURNING *;

-- name: GetRunbook :one
SELECT * FROM runbooks WHERE id = ? AND project_id = ?;

-- name: GetRunbookByID :one
SELECT * FROM runbooks WHERE id = ?;

-- name: ListRunbooks :many
SELECT * FROM runbooks WHERE project_id = ? ORDER BY name, id;

-- name: CreateRunbookVersion :one
INSERT INTO runbook_versions (runbook_id, version, release_id)
VALUES (?, ?, ?) RETURNING *;

-- name: GetRunbookVersion :one
SELECT * FROM runbook_versions WHERE id = ? AND runbook_id = ?;

-- name: GetLatestRunbookVersion :one
SELECT * FROM runbook_versions WHERE runbook_id = ?
ORDER BY version DESC LIMIT 1;

-- name: ListRunbookVersions :many
SELECT * FROM runbook_versions WHERE runbook_id = ? ORDER BY version DESC;

-- name: SetRunbookReleaseKind :exec
UPDATE releases SET kind = 'runbook' WHERE id = ?;

-- name: SetRunbookDeploymentKind :exec
UPDATE deployments SET kind = 'runbook' WHERE id = ?;

-- name: CreateRunbookExecution :one
INSERT INTO runbook_executions
    (runbook_version_id, deployment_id, actor_user_id, schedule_id)
VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetRunbookExecution :one
SELECT x.*, d.environment_id, d.status, d.started_at, d.finished_at,
    v.runbook_id, v.version, b.project_id, e.name AS environment_name
FROM runbook_executions x
JOIN runbook_versions v ON v.id = x.runbook_version_id
JOIN runbooks b ON b.id = v.runbook_id
JOIN deployments d ON d.id = x.deployment_id
JOIN environments e ON e.id = d.environment_id
WHERE x.id = ? AND b.project_id = ?;

-- name: ListRunbookExecutions :many
SELECT x.*, d.environment_id, d.status, d.started_at, d.finished_at,
    v.runbook_id, v.version, b.project_id, b.name AS runbook_name,
    e.name AS environment_name
FROM runbook_executions x
JOIN runbook_versions v ON v.id = x.runbook_version_id
JOIN runbooks b ON b.id = v.runbook_id
JOIN deployments d ON d.id = x.deployment_id
JOIN environments e ON e.id = d.environment_id
WHERE b.project_id = ? ORDER BY x.id DESC;

-- name: GetRunbookExecutionByDeployment :one
SELECT x.*, v.runbook_id, b.project_id
FROM runbook_executions x
JOIN runbook_versions v ON v.id = x.runbook_version_id
JOIN runbooks b ON b.id = v.runbook_id
WHERE x.deployment_id = ?;

-- name: CreateRunbookSchedule :one
INSERT INTO runbook_schedules
    (runbook_id, version_id, environment_id, cron, next_run_at)
VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: GetRunbookSchedule :one
SELECT * FROM runbook_schedules WHERE id = ? AND runbook_id = ?;

-- name: ListRunbookSchedules :many
SELECT * FROM runbook_schedules WHERE runbook_id = ? ORDER BY id DESC;

-- name: ListDueRunbookSchedules :many
SELECT * FROM runbook_schedules WHERE enabled = 1 AND next_run_at <= ?
ORDER BY next_run_at, id;

-- name: AdvanceRunbookSchedule :execrows
UPDATE runbook_schedules SET next_run_at = ?, last_fired_at = ?
WHERE id = ? AND enabled = 1 AND next_run_at <= ?;

-- name: SkipRunbookSchedule :execrows
UPDATE runbook_schedules SET next_run_at = ?
WHERE id = ? AND enabled = 1 AND next_run_at <= ?;

-- name: HasActiveRunbookScheduleExecution :one
SELECT EXISTS (
    SELECT 1 FROM runbook_executions x
    JOIN deployments d ON d.id = x.deployment_id
    WHERE x.schedule_id = ? AND d.status IN ('pending', 'running', 'pending_approval')
);

-- name: DisableRunbookSchedule :exec
UPDATE runbook_schedules SET enabled = 0 WHERE id = ?;

-- name: DeleteProjectRunbookExecutions :exec
DELETE FROM runbook_executions WHERE runbook_version_id IN (
    SELECT v.id FROM runbook_versions v
    JOIN runbooks b ON b.id = v.runbook_id WHERE b.project_id = ?
);

-- name: DeleteProjectRunbookSchedules :exec
DELETE FROM runbook_schedules WHERE runbook_id IN (
    SELECT id FROM runbooks WHERE project_id = ?
);

-- name: DeleteProjectRunbookVersions :exec
DELETE FROM runbook_versions WHERE runbook_id IN (
    SELECT id FROM runbooks WHERE project_id = ?
);

-- name: DeleteProjectRunbookReleases :exec
DELETE FROM releases WHERE project_id = ? AND kind = 'runbook';
