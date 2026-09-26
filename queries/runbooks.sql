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
WHERE b.project_id = ? ORDER BY x.id DESC
LIMIT ? OFFSET ?;

-- name: CountRunbookExecutions :one
SELECT COUNT(*) FROM runbook_executions x
JOIN runbook_versions v ON v.id = x.runbook_version_id
JOIN runbooks b ON b.id = v.runbook_id
WHERE b.project_id = ?;

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
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM runbook_executions x
    JOIN deployments d ON d.id = x.deployment_id
    WHERE x.schedule_id = ?
      AND (d.status IN ('pending', 'running', 'pending_approval')
        OR EXISTS (SELECT 1 FROM remote_deployment_claims c
                   WHERE c.deployment_id = d.id
                     AND c.state IN ('lost', 'cancel_unconfirmed'))
        OR EXISTS (SELECT 1 FROM remote_step_runs s
                   WHERE s.deployment_id = d.id
                     AND s.state IN ('lost', 'cancel_unconfirmed')))
) THEN 1 ELSE 0 END;

-- name: DisableRunbookSchedule :exec
UPDATE runbook_schedules SET enabled = 0 WHERE id = ?;

-- name: DeleteProjectRunbookExecutions :exec
DELETE FROM runbook_executions WHERE runbook_version_id IN (
    SELECT v.id FROM runbook_versions v
    JOIN runbooks b ON b.id = v.runbook_id WHERE b.project_id = ?
);

-- name: ListProjectRunbookDeploymentIDs :many
SELECT x.deployment_id FROM runbook_executions x
JOIN runbook_versions v ON v.id = x.runbook_version_id
JOIN runbooks b ON b.id = v.runbook_id
WHERE b.project_id = ?;

-- name: HasActiveProjectRunbookExecution :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM runbook_executions x
    JOIN runbook_versions v ON v.id = x.runbook_version_id
    JOIN runbooks b ON b.id = v.runbook_id
    JOIN deployments d ON d.id = x.deployment_id
    WHERE b.project_id = ?
      AND (d.status IN ('pending', 'running', 'pending_approval')
        OR EXISTS (SELECT 1 FROM remote_deployment_claims c
                   WHERE c.deployment_id = d.id
                     AND c.state IN ('lost', 'cancel_unconfirmed'))
        OR EXISTS (SELECT 1 FROM remote_step_runs s
                   WHERE s.deployment_id = d.id
                     AND s.state IN ('lost', 'cancel_unconfirmed')))
) THEN 1 ELSE 0 END;

-- name: DeleteRunbookRemoteStepLogSequences :exec
DELETE FROM remote_step_log_sequences WHERE deployment_id = ?;

-- name: DeleteRunbookRemoteStepRuns :exec
DELETE FROM remote_step_runs WHERE deployment_id = ?;

-- name: DeleteRunbookLogScopes :exec
DELETE FROM deployment_log_scopes WHERE deployment_id = ?;

-- name: DeleteRunbookDispatches :exec
DELETE FROM deployment_dispatches WHERE deployment_id = ?;

-- name: DeleteRunbookStepAttempts :exec
DELETE FROM deployment_step_attempts WHERE deployment_id = ?;

-- name: DeleteRunbookStepSelectors :exec
DELETE FROM deployment_step_selectors WHERE deployment_id = ?;

-- name: DeleteRunbookSteps :exec
DELETE FROM deployment_steps WHERE deployment_id = ?;

-- name: DeleteRunbookStepSource :exec
DELETE FROM deployment_step_sources WHERE deployment_id = ?;

-- name: DeleteRunbookRemoteClaim :exec
DELETE FROM remote_deployment_claims WHERE deployment_id = ?;

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
