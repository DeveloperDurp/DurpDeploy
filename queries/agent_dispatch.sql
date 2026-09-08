-- name: LockClaimAgent :execrows
UPDATE agents SET updated_at = updated_at WHERE id = ? AND status = 'active';

-- name: ListEligibleStepAgents :many
SELECT a.* FROM agents a WHERE a.status = 'active' AND a.last_heartbeat_at >= sqlc.arg(fresh_after)
      AND EXISTS (SELECT 1 FROM agent_pairings p WHERE p.agent_id = a.id AND p.state = 'paired')
      AND EXISTS (SELECT 1 FROM environment_agent_assignments e
          JOIN deployments d ON d.environment_id = e.environment_id
          WHERE d.id = sqlc.arg(deployment_id) AND e.agent_id = a.id)
      AND EXISTS (SELECT 1 FROM deployment_step_selectors s JOIN agent_labels l ON l.label = s.label
              WHERE s.deployment_id = sqlc.arg(deployment_id) AND s.step_index = sqlc.arg(step_index) AND l.agent_id = a.id)
      AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts busy WHERE busy.agent_id = a.id
          AND busy.state IN ('claimed', 'started', 'cancel_requested', 'cancel_uncertain', 'lost')) ORDER BY a.id;

-- name: ClaimRemoteDeploymentStep :execrows
UPDATE deployment_step_attempts SET state = 'claimed', agent_id = sqlc.arg(agent_id),
    claim_token_hash = sqlc.arg(claim_token_hash), claim_expires_at = sqlc.arg(claim_expires_at),
    last_heartbeat_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index) AND deployment_step_attempts.attempt = sqlc.arg(attempt)
  AND state = 'waiting' AND started_at IS NULL AND agent_id IS NULL
  AND wait_deadline > sqlc.arg(now) AND sqlc.arg(claim_expires_at) > sqlc.arg(now)
  AND EXISTS (SELECT 1 FROM deployments d WHERE d.id = deployment_step_attempts.deployment_id AND d.status IN ('pending', 'running'))
  AND EXISTS (SELECT 1 FROM deployment_steps s WHERE s.deployment_id = deployment_step_attempts.deployment_id
      AND s.step_index = deployment_step_attempts.step_index AND s.execution_target = 'agent')
  AND EXISTS (SELECT 1 FROM agents a WHERE a.id = sqlc.arg(agent_id) AND a.status = 'active' AND a.last_heartbeat_at >= sqlc.arg(fresh_after)
      AND EXISTS (SELECT 1 FROM agent_pairings p WHERE p.agent_id = a.id AND p.state = 'paired')
      AND EXISTS (SELECT 1 FROM environment_agent_assignments e
          JOIN deployments d ON d.environment_id = e.environment_id
          WHERE d.id = sqlc.arg(deployment_id) AND e.agent_id = a.id)
      AND EXISTS (SELECT 1 FROM deployment_step_selectors s JOIN agent_labels l ON l.label = s.label
              WHERE s.deployment_id = sqlc.arg(deployment_id) AND s.step_index = sqlc.arg(step_index) AND l.agent_id = a.id)
      AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts busy WHERE busy.agent_id = a.id
          AND busy.state IN ('claimed', 'started', 'cancel_requested', 'cancel_uncertain', 'lost')));

-- name: ExpireRemoteClaims :execrows
UPDATE deployment_step_attempts SET state = 'waiting', agent_id = NULL, claim_token_hash = NULL,
    claim_expires_at = NULL, last_heartbeat_at = NULL, updated_at = sqlc.arg(now)
WHERE state = 'claimed' AND started_at IS NULL AND claim_expires_at <= sqlc.arg(now);

-- name: StartRemoteDeploymentStep :execrows
UPDATE deployment_step_attempts SET state = 'started', started_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index)
  AND deployment_step_attempts.attempt = sqlc.arg(attempt) AND deployment_step_attempts.agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state = 'claimed' AND started_at IS NULL
  AND claim_expires_at > sqlc.arg(now) AND EXISTS (SELECT 1 FROM deployments d WHERE d.id = deployment_step_attempts.deployment_id AND d.status IN ('pending', 'running')) AND EXISTS (SELECT 1 FROM agents a WHERE a.id = deployment_step_attempts.agent_id AND a.status = 'active'
      AND EXISTS (SELECT 1 FROM agent_pairings p WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: HeartbeatRemoteDeploymentStep :execrows
UPDATE deployment_step_attempts SET last_heartbeat_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index)
  AND deployment_step_attempts.attempt = sqlc.arg(attempt) AND deployment_step_attempts.agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state IN ('started', 'cancel_requested')
  AND last_heartbeat_at <= sqlc.arg(now) AND EXISTS (SELECT 1 FROM agents a WHERE a.id = deployment_step_attempts.agent_id AND a.status = 'active'
      AND EXISTS (SELECT 1 FROM agent_pairings p WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: FinishRemoteDeploymentStep :execrows
UPDATE deployment_step_attempts SET state = sqlc.arg(state), reason = sqlc.narg(reason),
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index)
  AND deployment_step_attempts.attempt = sqlc.arg(attempt) AND deployment_step_attempts.agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state = 'started' AND started_at IS NOT NULL
  AND (sqlc.arg(state) = 'succeeded' OR sqlc.arg(state) = 'failed') AND EXISTS (SELECT 1 FROM deployments d WHERE d.id = deployment_step_attempts.deployment_id AND d.status IN ('pending', 'running')) AND EXISTS (SELECT 1 FROM agents a WHERE a.id = deployment_step_attempts.agent_id AND a.status = 'active'
      AND EXISTS (SELECT 1 FROM agent_pairings p WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: LockRemoteLogAttempt :execrows
UPDATE deployment_step_attempts SET updated_at = updated_at
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index)
  AND deployment_step_attempts.attempt = sqlc.arg(attempt) AND deployment_step_attempts.agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state IN ('started', 'cancel_requested') AND EXISTS (SELECT 1 FROM agents a WHERE a.id = deployment_step_attempts.agent_id AND a.status = 'active'
      AND EXISTS (SELECT 1 FROM agent_pairings p WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: RequestDeploymentStepCancellation :execrows
UPDATE deployment_step_attempts SET
    state = CASE WHEN started_at IS NULL THEN 'cancelled' ELSE 'cancel_requested' END,
    finished_at = CASE WHEN started_at IS NULL THEN sqlc.arg(now) ELSE NULL END,
    cancel_requested_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id) AND state IN ('waiting', 'claimed', 'started');

-- name: AcknowledgeRemoteCancellation :execrows
UPDATE deployment_step_attempts SET state = 'cancelled', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index)
  AND deployment_step_attempts.attempt = sqlc.arg(attempt) AND deployment_step_attempts.agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state = 'cancel_requested';

-- name: ExpireRemoteCancellation :execrows
UPDATE deployment_step_attempts SET state = 'cancel_uncertain', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE state = 'cancel_requested' AND agent_id IS NOT NULL AND cancel_requested_at <= sqlc.arg(stale_before);

-- name: LoseStaleRemoteAttempts :execrows
UPDATE deployment_step_attempts SET state = 'lost', reason = 'heartbeat_lost', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE state = 'started' AND agent_id IS NOT NULL AND last_heartbeat_at <= sqlc.arg(stale_before);

-- name: ExpireWaitingDeploymentSteps :execrows
UPDATE deployment_step_attempts SET state = 'failed', reason = 'wait_expired', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE state = 'waiting' AND wait_deadline <= sqlc.arg(now);

-- name: CreateDeploymentDispatch :execrows
INSERT INTO deployment_dispatches (deployment_id, step_index, attempt, ciphertext)
SELECT sqlc.arg(deployment_id), sqlc.arg(step_index), sqlc.arg(attempt), sqlc.narg(ciphertext)
WHERE EXISTS (SELECT 1 FROM deployment_step_attempts WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index)
  AND deployment_step_attempts.attempt = sqlc.arg(attempt) AND deployment_step_attempts.agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash) AND state = 'claimed')
  AND NOT EXISTS (SELECT 1 FROM deployment_dispatches WHERE deployment_id = sqlc.arg(deployment_id)
      AND step_index = sqlc.arg(step_index) AND attempt = sqlc.arg(attempt));

-- name: GetDeploymentDispatch :one
SELECT d.* FROM deployment_dispatches d JOIN deployment_step_attempts a
ON a.deployment_id = d.deployment_id AND a.step_index = d.step_index AND a.attempt = d.attempt
WHERE a.deployment_id = ? AND a.step_index = ? AND a.attempt = ? AND a.agent_id = ? AND a.claim_token_hash = ?;
