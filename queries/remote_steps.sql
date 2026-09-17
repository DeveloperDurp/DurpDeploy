-- name: CreateRemoteStepRuns :execrows
INSERT INTO remote_step_runs (deployment_id, step_index, agent_id)
SELECT s.deployment_id, s.step_index, a.id
FROM deployment_steps s
JOIN deployments d ON d.id = s.deployment_id
JOIN agents a ON a.status = 'active' AND a.revoked_at IS NULL
JOIN agent_pairings p ON p.agent_id = a.id AND p.state = 'paired'
JOIN agent_environment_labels e ON e.agent_id = a.id
    AND e.environment_id = d.environment_id
WHERE s.deployment_id = sqlc.arg(deployment_id)
  AND s.step_index = sqlc.arg(step_index)
  AND s.execution_target = 'agent'
  AND d.status = 'running'
  AND NOT EXISTS (
      SELECT 1 FROM deployment_step_selectors wanted
      WHERE wanted.deployment_id = s.deployment_id
        AND wanted.step_index = s.step_index
        AND NOT EXISTS (
            SELECT 1 FROM agent_labels owned
            WHERE owned.agent_id = a.id
              AND lower(owned.label) = lower(wanted.label)
        )
  )
  AND NOT EXISTS (
      SELECT 1 FROM remote_step_runs existing
      WHERE existing.deployment_id = s.deployment_id
        AND existing.step_index = s.step_index
        AND existing.agent_id = a.id
  );

-- name: ListRemoteStepRuns :many
SELECT * FROM remote_step_runs
WHERE deployment_id = ? AND step_index = ? ORDER BY agent_id;

-- name: ListWaitingRemoteStepRuns :many
SELECT r.* FROM remote_step_runs r
JOIN deployments d ON d.id = r.deployment_id
WHERE r.agent_id = sqlc.arg(agent_id) AND r.state = 'waiting'
  AND d.status = 'running'
  AND NOT EXISTS (
      SELECT 1 FROM remote_deployment_claims legacy
      WHERE legacy.agent_id = r.agent_id
        AND legacy.state IN ('claimed', 'started', 'cancel_requested')
  )
ORDER BY r.created_at, r.deployment_id, r.step_index;

-- name: ClaimRemoteStepRun :execrows
UPDATE remote_step_runs SET state = 'claimed',
    claim_token_hash = sqlc.arg(claim_token_hash),
    ciphertext = sqlc.arg(ciphertext),
    claim_expires_at = sqlc.arg(claim_expires_at),
    last_heartbeat_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index)
  AND agent_id = sqlc.arg(agent_id) AND state = 'waiting';

-- name: ExpireRemoteStepClaims :execrows
UPDATE remote_step_runs SET state = 'waiting', claim_token_hash = NULL,
    ciphertext = NULL, claim_expires_at = NULL, last_heartbeat_at = NULL,
    updated_at = sqlc.arg(now)
WHERE state = 'claimed' AND started_at IS NULL
  AND claim_expires_at <= sqlc.arg(now);

-- name: FailStaleRemoteStepCancellations :execrows
UPDATE remote_step_runs SET state = 'failed', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE state = 'cancel_requested'
  AND cancel_requested_at <= sqlc.arg(stale_before);

-- name: GetRemoteStepRunByClaim :one
SELECT * FROM remote_step_runs
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: StartRemoteStepRun :execrows
UPDATE remote_step_runs SET state = 'started', started_at = sqlc.arg(now),
    last_heartbeat_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index) AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state = 'claimed';

-- name: HeartbeatRemoteStepRun :execrows
UPDATE remote_step_runs SET last_heartbeat_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index) AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND state IN ('started', 'cancel_requested');

-- name: FinishRemoteStepRun :execrows
UPDATE remote_step_runs SET state = sqlc.arg(state), finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index) AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state = 'started';

-- name: RequestRemoteStepCancellation :execrows
UPDATE remote_step_runs SET
    state = CASE WHEN state = 'waiting' THEN 'cancelled'
        ELSE 'cancel_requested' END,
    cancel_requested_at = sqlc.arg(now),
    finished_at = CASE WHEN state = 'waiting' THEN sqlc.arg(now)
        ELSE finished_at END,
    updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND state IN ('waiting', 'claimed', 'started');

-- name: AcknowledgeRemoteStepCancellation :execrows
UPDATE remote_step_runs SET state = 'cancelled', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index) AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND state = 'cancel_requested';

-- name: FailUnfinishedRemoteStepRuns :execrows
UPDATE remote_step_runs SET state = 'failed', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index)
  AND state IN ('waiting', 'claimed', 'started', 'cancel_requested');

-- name: GetRemoteStepLogBySequence :one
SELECT l.* FROM deployment_logs l
JOIN remote_step_log_sequences s ON s.log_id = l.id
WHERE s.deployment_id = ? AND s.step_index = ? AND s.agent_id = ?
  AND s.sequence = ?;

-- name: CreateRemoteStepLogSequence :exec
INSERT INTO remote_step_log_sequences
    (deployment_id, step_index, agent_id, sequence, log_id)
VALUES (?, ?, ?, ?, ?);
