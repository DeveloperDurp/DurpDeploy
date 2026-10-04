-- name: LockDeploymentEnvironment :execrows
UPDATE environments SET name = name -- NOSONAR: serialize queue transactions
WHERE id = ?;

-- name: GetEnvironmentDeploymentSlot :one
SELECT deployment_id FROM environment_deployment_slots WHERE environment_id = ?;

-- name: CreateEnvironmentDeploymentSlot :exec
INSERT INTO environment_deployment_slots (environment_id, deployment_id) VALUES (?, ?);

-- name: DeleteEnvironmentDeploymentSlot :exec
DELETE FROM environment_deployment_slots WHERE environment_id = ?;

-- name: ListDeploymentQueueEnvironments :many
SELECT environment_id FROM deployments WHERE status IN ('queued', 'pending', 'running', 'cleanup_unconfirmed')
UNION SELECT environment_id FROM environment_deployment_slots
ORDER BY environment_id;

-- name: CancelWaitingQueuedRemoteClaim :exec
UPDATE remote_deployment_claims SET state = 'cancelled', finished_at = unixepoch(), updated_at = unixepoch()
WHERE deployment_id = ? AND state = 'waiting';

-- name: ListEnvironmentQueueBlockers :many
SELECT d.id FROM deployments d
WHERE d.environment_id = ? AND (
    d.status IN ('pending', 'running', 'cleanup_unconfirmed')
    OR EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = d.id
        AND c.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed'))
    OR EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = d.id
        AND s.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed'))
)
ORDER BY d.created_at, d.id;

-- name: GetNextQueuedDeployment :one
SELECT id FROM deployments WHERE environment_id = ? AND status = 'queued'
ORDER BY created_at, id LIMIT 1;

-- name: AdmitQueuedDeployment :execrows
UPDATE deployments SET status = 'pending' WHERE id = ? AND status = 'queued';

-- name: StartQueuedLocalDeployment :execrows
UPDATE deployments SET status = 'running', started_at = unixepoch()
WHERE id = ? AND status = 'pending' AND assigned_agent_id IS NULL
AND EXISTS (SELECT 1 FROM environment_deployment_slots s
    WHERE s.deployment_id = deployments.id AND s.environment_id = deployments.environment_id);

-- name: CancelQueuedDeployment :execrows
UPDATE deployments SET status = 'cancelled', finished_at = unixepoch()
WHERE id = ? AND status IN ('queued', 'pending_approval', 'pending')
AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = deployments.id
    AND c.state NOT IN ('waiting', 'cancelled'));

-- name: GetDeploymentQueuePosition :one
SELECT COUNT(*) FROM deployments ahead JOIN deployments target ON target.id = sqlc.arg(deployment_id)
WHERE target.status = 'queued' AND ahead.environment_id = target.environment_id
AND ahead.status = 'queued'
AND (ahead.created_at < target.created_at OR (ahead.created_at = target.created_at AND ahead.id <= target.id));

-- name: GetVisibleEnvironmentDeploymentSlot :one
SELECT d.id, d.kind, r.project_id, x.id AS runbook_execution_id
FROM environment_deployment_slots s
JOIN deployments d ON d.id = s.deployment_id
JOIN releases r ON r.id = d.release_id
LEFT JOIN runbook_executions x ON x.deployment_id = d.id
WHERE s.environment_id = sqlc.arg(environment_id)
AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (
    SELECT 1 FROM project_members p WHERE p.project_id = r.project_id AND p.user_id = sqlc.arg(user_id)));
