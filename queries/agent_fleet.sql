-- name: ListFleetHealthBaselines :many
SELECT a.id, CAST(COALESCE(p.paired_at, a.created_at) AS INTEGER) AS baseline
FROM agents a LEFT JOIN agent_pairings p ON p.agent_id = a.id;

-- name: ListFleetInterpreters :many
SELECT agent_id, interpreter FROM agent_interpreters
ORDER BY agent_id, interpreter;

-- name: ListFleetExecutionCapabilities :many
SELECT agent_id, 'mode' AS kind, execution_mode AS value FROM agent_execution_modes
UNION ALL SELECT agent_id, 'runtime' AS kind, runtime AS value FROM agent_container_runtimes
UNION ALL SELECT agent_id, 'interpreter' AS kind, interpreter AS value FROM agent_container_interpreters
ORDER BY agent_id, kind, value;

-- name: ListFleetCurrentWork :many
SELECT agent_id, deployment_id, step_index, state FROM remote_step_runs
WHERE state IN ('claimed', 'started', 'cancel_requested')
    OR (state = 'cleanup_unconfirmed' AND cleanup_confirmed_at IS NULL)
UNION ALL
SELECT agent_id, deployment_id, CAST(-1 AS INTEGER) AS step_index, state
FROM remote_deployment_claims
WHERE state IN ('claimed', 'started', 'cancel_requested')
    OR (state = 'cleanup_unconfirmed' AND cleanup_confirmed_at IS NULL)
ORDER BY agent_id, deployment_id, step_index;

-- name: ListFleetQueuedWork :many
SELECT agent_id, CAST(COUNT(*) AS INTEGER) AS queued_work FROM (
    SELECT agent_id FROM remote_step_runs WHERE state = 'waiting'
    UNION ALL
    SELECT agent_id FROM remote_deployment_claims WHERE state = 'waiting'
) queued GROUP BY agent_id;

-- name: ListFleetLastSuccessfulDeployments :many
SELECT agent_id, deployment_id, finished_at FROM (
    SELECT agent_id, deployment_id, finished_at,
        ROW_NUMBER() OVER (PARTITION BY agent_id
            ORDER BY finished_at DESC, deployment_id DESC) AS ordinal
    FROM (
        SELECT r.agent_id, d.id AS deployment_id, d.finished_at
        FROM remote_step_runs r JOIN deployments d ON d.id = r.deployment_id
        WHERE r.state = 'succeeded' AND d.status = 'succeeded'
        UNION
        SELECT c.agent_id, d.id AS deployment_id, d.finished_at
        FROM remote_deployment_claims c JOIN deployments d ON d.id = c.deployment_id
        WHERE c.state = 'succeeded' AND d.status = 'succeeded'
    ) successes
) ranked WHERE ordinal = 1;

-- name: ListFleetLastErrors :many
SELECT agent_id, deployment_id, step_index, state, reason, finished_at FROM (
    SELECT agent_id, deployment_id, step_index, state, reason, finished_at,
        ROW_NUMBER() OVER (PARTITION BY agent_id ORDER BY finished_at DESC,
            deployment_id DESC, step_index DESC) AS ordinal
    FROM (
        SELECT agent_id, deployment_id, step_index, state, state AS reason, finished_at
        FROM remote_step_runs WHERE state IN ('failed', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed')
        UNION ALL
        SELECT agent_id, deployment_id, CAST(-1 AS INTEGER) AS step_index, state,
            COALESCE(reason, state) AS reason, finished_at
        FROM remote_deployment_claims
        WHERE state IN ('failed', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed')
    ) failures
) ranked WHERE ordinal = 1;
