-- name: CreateRemoteStepRuns :execrows
INSERT INTO remote_step_runs (deployment_id, step_index, agent_id)
SELECT s.deployment_id, s.step_index, s.agent_id
FROM eligible_remote_step_agents s JOIN deployments d ON d.id = s.deployment_id
WHERE s.deployment_id = sqlc.arg(deployment_id)
  AND s.step_index = sqlc.arg(step_index)
  AND EXISTS (SELECT 1 FROM deployment_steps ds
      WHERE ds.deployment_id = s.deployment_id
        AND ds.step_index = s.step_index AND ds.kind = 'script')
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
SELECT r.agent_id, r.state, r.started_at, a.draining FROM remote_step_runs r
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
      AND ((busy.state = 'cleanup_unconfirmed' AND busy.cleanup_confirmed_at IS NULL)
        OR (busy.state = 'cancel_requested' AND busy.started_at IS NULL
          AND busy.prestart_input = 1)))
  AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims busy WHERE busy.agent_id = r.agent_id
      AND busy.state = 'cleanup_unconfirmed' AND busy.cleanup_confirmed_at IS NULL)
ORDER BY r.created_at ASC, r.deployment_id ASC, r.step_index ASC;

-- name: ClaimRemoteStepRun :execrows
UPDATE remote_step_runs SET state = 'claimed',
    prestart_input = 0,
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
  )
  AND NOT EXISTS (SELECT 1 FROM remote_step_runs busy
      WHERE busy.agent_id = remote_step_runs.agent_id
        AND ((busy.state = 'cleanup_unconfirmed' AND busy.cleanup_confirmed_at IS NULL)
          OR (busy.state = 'cancel_requested' AND busy.started_at IS NULL
            AND busy.prestart_input = 1)))
  AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims busy
      WHERE busy.agent_id = remote_step_runs.agent_id
        AND busy.state = 'cleanup_unconfirmed' AND busy.cleanup_confirmed_at IS NULL);

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

-- name: FailInvalidWaitingRemoteSteps :exec
UPDATE remote_step_runs SET state = 'failed',
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id) AND state = 'waiting';

-- name: FailInvalidRemoteStepDeployment :exec
UPDATE deployments SET status = 'failed', finished_at = sqlc.arg(now)
WHERE id = sqlc.arg(deployment_id) AND status = 'running'
  AND assigned_agent_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM remote_step_runs r
      WHERE r.deployment_id = deployments.id
        AND r.state IN ('waiting', 'claimed', 'started', 'cancel_requested'));

-- name: ListRetriedRemoteStepClaimExpirations :many
SELECT DISTINCT deployment_id FROM remote_step_runs
WHERE state = 'claimed' AND started_at IS NULL
  AND claim_expires_at <= sqlc.arg(now) AND expired_claims > 0
  AND prestart_input = 0
ORDER BY deployment_id ASC;

-- name: ExpireRemoteStepClaims :many
UPDATE remote_step_runs SET
    state = CASE WHEN expired_claims = 0 THEN 'waiting' ELSE 'failed' END,
    finished_at = CASE WHEN expired_claims = 0 THEN NULL ELSE sqlc.arg(now) END,
    expired_claims = expired_claims + 1,
    claim_token_hash = NULL,
    ciphertext = NULL, claim_expires_at = NULL, last_heartbeat_at = NULL,
    updated_at = sqlc.arg(now)
WHERE state = 'claimed' AND started_at IS NULL
  AND claim_expires_at <= sqlc.arg(now) AND prestart_input = 0
RETURNING deployment_id, agent_id, state;

-- name: CancelExpiredPrestartTransfers :exec
UPDATE remote_step_runs SET state = 'cancel_requested',
    cancel_requested_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE state = 'claimed' AND started_at IS NULL
  AND claim_expires_at <= sqlc.arg(now) AND prestart_input = 1;

-- name: ExpireRemoteStepCancellations :execrows
UPDATE remote_step_runs SET
    state = CASE WHEN started_at IS NULL AND prestart_input = 1
        THEN 'cleanup_unconfirmed' ELSE 'cancel_unconfirmed' END,
    finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now), log_buffer_ciphertext = NULL
WHERE state = 'cancel_requested'
  AND cancel_requested_at <= sqlc.arg(stale_before);

-- name: LoseStaleRemoteStepRuns :execrows
UPDATE remote_step_runs SET state = 'lost', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now), log_buffer_ciphertext = NULL
WHERE state = 'started'
  AND last_heartbeat_at <= sqlc.arg(stale_before);

-- name: GetRemoteStepRunByClaim :one
SELECT deployment_id, step_index, agent_id, state, claim_token_hash, ciphertext, claim_expires_at, last_heartbeat_at, started_at, finished_at, cancel_requested_at, created_at, updated_at, log_buffer_ciphertext, recovery_cancelled, cleanup_confirmed_at, expired_claims, prestart_input FROM remote_step_runs
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
  AND (state IN ('started', 'lost', 'cancel_unconfirmed')
      OR (state = 'cancel_requested' AND sqlc.arg(state) = 'cleanup_unconfirmed'));

-- name: RequestRemoteStepCancellation :execrows
UPDATE remote_step_runs SET
    state = CASE WHEN state = 'waiting' OR (state = 'claimed' AND prestart_input = 0)
        THEN 'cancelled'
        ELSE 'cancel_requested' END,
    cancel_requested_at = sqlc.arg(now),
    finished_at = CASE WHEN state = 'waiting' OR (state = 'claimed' AND prestart_input = 0)
        THEN sqlc.arg(now)
        ELSE finished_at END,
    updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND state IN ('waiting', 'claimed', 'started');

-- name: MarkPrestartRemoteStepInput :exec
UPDATE remote_step_runs SET prestart_input = 1
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index) AND agent_id = sqlc.arg(agent_id)
  AND state = 'claimed';

-- name: AcknowledgeRemoteStepCancellation :execrows
UPDATE remote_step_runs SET
    state = CASE WHEN state = 'cleanup_unconfirmed' THEN state ELSE 'cancelled' END,
    finished_at = CASE WHEN state = 'cleanup_unconfirmed'
        THEN finished_at ELSE sqlc.arg(now) END,
    cleanup_confirmed_at = CASE WHEN state = 'cleanup_unconfirmed'
        THEN COALESCE(cleanup_confirmed_at, sqlc.arg(now)) ELSE cleanup_confirmed_at END,
    updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND step_index = sqlc.arg(step_index) AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND (state IN ('cancel_requested', 'cancel_unconfirmed')
      OR (state = 'cleanup_unconfirmed' AND started_at IS NULL AND prestart_input = 1));

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
