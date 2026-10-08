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
SELECT environment_id FROM deployments WHERE status IN ('queued', 'pending', 'running', 'publishing_artifact', 'awaiting_artifact_approval', 'cleanup_unconfirmed')
UNION SELECT environment_id FROM environment_deployment_slots
ORDER BY environment_id;

-- name: CancelWaitingQueuedRemoteClaim :exec
UPDATE remote_deployment_claims SET state = 'cancelled', finished_at = unixepoch(), updated_at = unixepoch()
WHERE deployment_id = ? AND state = 'waiting';

-- name: ListEnvironmentQueueBlockers :many
SELECT d.id FROM deployments d
WHERE d.environment_id = ? AND (
    d.status IN ('pending', 'running', 'publishing_artifact', 'awaiting_artifact_approval')
    OR (d.status = 'cleanup_unconfirmed'
        AND (d.container_namespace IS NOT NULL OR (
        d.cleanup_confirmed_at IS NULL AND
        NOT EXISTS (SELECT 1 FROM remote_deployment_claims c
            WHERE c.deployment_id = d.id AND c.state = 'cleanup_unconfirmed')
        AND NOT EXISTS (SELECT 1 FROM remote_step_runs s
            WHERE s.deployment_id = d.id AND s.state = 'cleanup_unconfirmed'))))
    OR EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = d.id
        AND c.state = 'cleanup_unconfirmed' AND c.cleanup_confirmed_at IS NULL)
    OR EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = d.id
        AND s.state = 'cleanup_unconfirmed' AND s.cleanup_confirmed_at IS NULL)
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
UPDATE deployments SET status = 'running', started_at = COALESCE(started_at, unixepoch())
WHERE id = ? AND status = 'pending' AND assigned_agent_id IS NULL
AND EXISTS (SELECT 1 FROM environment_deployment_slots s
    WHERE s.deployment_id = deployments.id AND s.environment_id = deployments.environment_id);

-- name: CancelQueuedDeployment :execrows
UPDATE deployments SET status = 'cancelled', finished_at = unixepoch()
WHERE id = ? AND status IN ('queued', 'pending_approval', 'pending')
AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = deployments.id
    AND c.state NOT IN ('waiting', 'cancelled'));

-- name: GetDeploymentQueueState :one
SELECT d.status,
    (SELECT COUNT(*) FROM deployments ahead
     WHERE d.status = 'queued' AND ahead.environment_id = d.environment_id
       AND ahead.status = 'queued'
       AND (ahead.created_at < d.created_at OR (ahead.created_at = d.created_at AND ahead.id <= d.id))) AS queue_position,
    active.id AS active_deployment_id, active.kind, r.project_id,
    x.id AS runbook_execution_id
FROM deployments d
LEFT JOIN environment_deployment_slots s ON s.environment_id = d.environment_id
LEFT JOIN deployments active ON active.id = s.deployment_id
    AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (
        SELECT 1 FROM releases visible JOIN project_members m ON m.project_id = visible.project_id
        WHERE visible.id = active.release_id AND m.user_id = sqlc.arg(user_id)))
LEFT JOIN releases r ON r.id = active.release_id
LEFT JOIN runbook_executions x ON x.deployment_id = active.id
WHERE d.id = sqlc.arg(deployment_id);
