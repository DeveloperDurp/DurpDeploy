-- name: ListDeploymentQueueSlots :many
SELECT deployment_id FROM environment_deployment_slots
ORDER BY environment_id ASC, project_id ASC;

-- name: CountDeploymentBufferedRemoteLogs :one
SELECT
  (SELECT COUNT(*) FROM remote_step_runs s WHERE s.deployment_id = sqlc.arg(deployment_id)
    AND NULLIF(s.log_buffer_ciphertext, '') IS NOT NULL)
  + (SELECT COUNT(*) FROM remote_deployment_claims c WHERE c.deployment_id = sqlc.arg(deployment_id)
    AND NULLIF(c.log_buffer_ciphertext, '') IS NOT NULL);

-- name: ListDeploymentRecoveryRuns :many
SELECT s.agent_id, s.state, s.cleanup_confirmed_at,
    COALESCE(a.status, 'missing') AS agent_status
FROM remote_step_runs s LEFT JOIN agents a ON a.id = s.agent_id
WHERE s.deployment_id = sqlc.arg(deployment_id)
UNION ALL
SELECT c.agent_id, c.state, c.cleanup_confirmed_at,
    COALESCE(a.status, 'missing') AS agent_status
FROM remote_deployment_claims c LEFT JOIN agents a ON a.id = c.agent_id
WHERE c.deployment_id = sqlc.arg(deployment_id)
ORDER BY agent_id ASC, state ASC;
