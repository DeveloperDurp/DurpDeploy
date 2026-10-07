-- name: CreateRemoteStepRuns :execrows
INSERT INTO remote_step_runs (deployment_id, step_index, agent_id)
SELECT s.deployment_id, s.step_index, s.agent_id
FROM eligible_remote_step_agents s JOIN deployments d ON d.id = s.deployment_id
WHERE s.deployment_id = sqlc.arg(deployment_id)
  AND s.step_index = sqlc.arg(step_index)
  AND d.status = 'running'
  AND EXISTS (SELECT 1 FROM environment_deployment_slots slot WHERE slot.deployment_id = d.id)
  AND NOT EXISTS (
      SELECT 1 FROM remote_step_runs existing
      WHERE existing.deployment_id = s.deployment_id
        AND existing.step_index = s.step_index
        AND existing.agent_id = s.agent_id
  );

-- name: ListRemoteStepRuns :many
SELECT * FROM remote_step_runs
WHERE deployment_id = ? AND step_index = ? ORDER BY agent_id;

-- name: ListRemoteStepRunsForRunner :many
SELECT r.agent_id, r.state, a.draining FROM remote_step_runs r
JOIN agents a ON a.id = r.agent_id
WHERE r.deployment_id = ? AND r.step_index = ? ORDER BY r.agent_id;

-- name: ListWaitingRemoteStepRuns :many
SELECT r.deployment_id, r.step_index FROM remote_step_runs r
JOIN deployments d ON d.id = r.deployment_id
JOIN eligible_remote_step_agents s ON s.deployment_id = r.deployment_id
    AND s.step_index = r.step_index AND s.agent_id = r.agent_id
WHERE r.agent_id = sqlc.arg(agent_id) AND r.state = 'waiting'
  AND d.status = 'running'
  AND EXISTS (SELECT 1 FROM environment_deployment_slots slot WHERE slot.deployment_id = d.id)
  AND NOT EXISTS (
      SELECT 1 FROM remote_deployment_claims legacy
      WHERE legacy.agent_id = r.agent_id
        AND legacy.state IN ('claimed', 'started', 'cancel_requested')
  )
  AND NOT EXISTS (SELECT 1 FROM remote_step_runs busy WHERE busy.agent_id = r.agent_id
      AND busy.state = 'cleanup_unconfirmed' AND busy.cleanup_confirmed_at IS NULL)
  AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims busy WHERE busy.agent_id = r.agent_id
      AND busy.state = 'cleanup_unconfirmed' AND busy.cleanup_confirmed_at IS NULL)
ORDER BY r.created_at, r.deployment_id, r.step_index;

-- name: ClaimRemoteStepRun :execrows
UPDATE remote_step_runs SET state = 'claimed',
    claim_token_hash = sqlc.arg(claim_token_hash),
    ciphertext = sqlc.arg(ciphertext),
    claim_expires_at = sqlc.arg(claim_expires_at),
    last_heartbeat_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE remote_step_runs.deployment_id = sqlc.arg(deployment_id)
  AND remote_step_runs.step_index = sqlc.arg(step_index)
  AND remote_step_runs.agent_id = sqlc.arg(agent_id)
  AND remote_step_runs.state = 'waiting'
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = remote_step_runs.agent_id
        AND a.status = 'active' AND a.draining = 0)
  AND EXISTS (
      SELECT 1 FROM eligible_remote_step_agents s
      WHERE s.agent_id = remote_step_runs.agent_id
        AND s.deployment_id = remote_step_runs.deployment_id
        AND s.step_index = remote_step_runs.step_index
  );

-- name: FailUnsupportedWaitingRemoteStepRuns :execrows
UPDATE remote_step_runs SET state = 'failed',
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE remote_step_runs.agent_id = sqlc.arg(agent_id)
  AND remote_step_runs.state = 'waiting'
  AND NOT EXISTS (
      SELECT 1 FROM eligible_remote_step_agents s
      WHERE s.agent_id = remote_step_runs.agent_id
        AND s.deployment_id = remote_step_runs.deployment_id
        AND s.step_index = remote_step_runs.step_index
  );

-- name: ExpireRemoteStepClaims :execrows
UPDATE remote_step_runs SET state = 'waiting', claim_token_hash = NULL,
    ciphertext = NULL, claim_expires_at = NULL, last_heartbeat_at = NULL,
    updated_at = sqlc.arg(now)
WHERE state = 'claimed' AND started_at IS NULL
  AND claim_expires_at <= sqlc.arg(now);

-- name: ExpireRemoteStepCancellations :execrows
UPDATE remote_step_runs SET state = 'cancel_unconfirmed', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now), log_buffer_ciphertext = NULL
WHERE state = 'cancel_requested'
  AND cancel_requested_at <= sqlc.arg(stale_before);

-- name: LoseStaleRemoteStepRuns :execrows
UPDATE remote_step_runs SET state = 'lost', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now), log_buffer_ciphertext = NULL
WHERE state = 'started'
  AND last_heartbeat_at <= sqlc.arg(stale_before);

-- name: GetRemoteStepRunByClaim :one
SELECT * FROM remote_step_runs
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: LockRemoteStepRun :execrows
UPDATE remote_step_runs SET updated_at = updated_at -- NOSONAR: intentional write lock
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: StartRemoteStepRun :execrows
UPDATE remote_step_runs SET state = 'started', started_at = sqlc.arg(now),
    last_heartbeat_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE remote_step_runs.deployment_id = sqlc.arg(deployment_id)
  AND remote_step_runs.step_index = sqlc.arg(step_index)
  AND remote_step_runs.agent_id = sqlc.arg(agent_id)
  AND remote_step_runs.claim_token_hash = sqlc.arg(claim_token_hash)
  AND remote_step_runs.state = 'claimed'
  AND remote_step_runs.claim_expires_at > sqlc.arg(now)
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = remote_step_runs.agent_id AND a.status = 'active'
        AND EXISTS (SELECT 1 FROM agent_pairings p
            WHERE p.agent_id = a.id AND p.state = 'paired'));

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
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND (state = 'started' OR (state = 'cancel_requested' AND sqlc.arg(state) = 'cleanup_unconfirmed'));

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

-- name: GetRemoteStepLogBySequence :one
SELECT l.* FROM deployment_logs l
JOIN remote_step_log_sequences s ON s.log_id = l.id
WHERE s.deployment_id = ? AND s.step_index = ? AND s.agent_id = ?
  AND s.sequence = ?;

-- name: GetLastRemoteStepLogSequence :one
SELECT CAST(COALESCE(MAX(sequence), -1) AS INTEGER) FROM remote_step_log_sequences
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index)
  AND agent_id = sqlc.arg(agent_id);

-- name: UpdateRemoteStepLogBuffer :execrows
UPDATE remote_step_runs
SET log_buffer_ciphertext = sqlc.narg(log_buffer_ciphertext)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: CreateRemoteStepLogSequence :exec
INSERT INTO remote_step_log_sequences
    (deployment_id, step_index, agent_id, sequence, log_id)
VALUES (?, ?, ?, ?, ?);

-- name: ConfirmRemoteStepCleanup :exec
UPDATE remote_step_runs SET cleanup_confirmed_at = sqlc.arg(now)
WHERE agent_id = sqlc.arg(agent_id) AND state = 'cleanup_unconfirmed'
  AND cleanup_confirmed_at IS NULL;
