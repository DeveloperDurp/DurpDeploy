-- name: ListAgentCurrentWork :many
SELECT r.deployment_id, r.step_index, r.state FROM remote_step_runs r
WHERE r.agent_id = sqlc.arg(agent_id)
  AND (r.state IN ('claimed', 'started', 'cancel_requested')
      OR (r.state = 'cleanup_unconfirmed' AND r.cleanup_confirmed_at IS NULL))
UNION ALL
SELECT c.deployment_id, CAST(-1 AS INTEGER) AS step_index, c.state
FROM remote_deployment_claims c
WHERE c.agent_id = sqlc.arg(agent_id)
  AND (c.state IN ('claimed', 'started', 'cancel_requested')
      OR (c.state = 'cleanup_unconfirmed' AND c.cleanup_confirmed_at IS NULL))
ORDER BY deployment_id, step_index;

-- name: CountAgentQueuedWork :one
SELECT CAST(
    (SELECT COUNT(*) FROM remote_step_runs r
     WHERE r.agent_id = sqlc.arg(agent_id) AND r.state = 'waiting') +
    (SELECT COUNT(*) FROM remote_deployment_claims c
     WHERE c.agent_id = sqlc.arg(agent_id) AND c.state = 'waiting')
    AS INTEGER) AS queued_work;

-- name: GetAgentLastSuccessfulDeployment :one
SELECT d.id AS deployment_id, d.finished_at FROM deployments d
WHERE d.status = 'succeeded' AND (
    EXISTS (SELECT 1 FROM remote_step_runs r
        WHERE r.deployment_id = d.id AND r.agent_id = sqlc.arg(agent_id)
          AND r.state = 'succeeded') OR
    EXISTS (SELECT 1 FROM remote_deployment_claims c
        WHERE c.deployment_id = d.id AND c.agent_id = sqlc.arg(agent_id)
          AND c.state = 'succeeded'))
ORDER BY d.finished_at DESC, d.id DESC LIMIT 1;

-- name: GetAgentLastError :one
SELECT deployment_id, step_index, state, reason, finished_at FROM (
    SELECT r.deployment_id, r.step_index, r.state, r.state AS reason, r.finished_at
    FROM remote_step_runs r
    WHERE r.agent_id = sqlc.arg(agent_id)
      AND r.state IN ('failed', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed')
    UNION ALL
    SELECT c.deployment_id, CAST(-1 AS INTEGER) AS step_index, c.state,
        COALESCE(c.reason, c.state) AS reason, c.finished_at
    FROM remote_deployment_claims c
    WHERE c.agent_id = sqlc.arg(agent_id)
      AND c.state IN ('failed', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed')
) failures ORDER BY finished_at DESC, deployment_id DESC, step_index DESC
LIMIT 1;
-- name: GetAgentHealthBaseline :one
SELECT CAST(COALESCE(p.paired_at, a.created_at) AS INTEGER) AS baseline
FROM agents a LEFT JOIN agent_pairings p ON p.agent_id = a.id
WHERE a.id = ?;
