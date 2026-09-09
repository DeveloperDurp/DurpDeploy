-- name: LockClaimAgent :execrows
UPDATE agents SET updated_at = updated_at WHERE id = ? AND status = 'active';

-- name: CreateRemoteDeploymentClaim :execrows
INSERT INTO remote_deployment_claims (deployment_id, agent_id)
SELECT d.id, d.assigned_agent_id FROM deployments d
WHERE d.id = sqlc.arg(deployment_id) AND d.assigned_agent_id IS NOT NULL
  AND d.status IN ('pending', 'pending_approval')
  AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c
      WHERE c.deployment_id = d.id);

-- name: GetRemoteDeploymentClaim :one
SELECT * FROM remote_deployment_claims WHERE deployment_id = ?;

-- name: ListWaitingRemoteDeploymentClaims :many
SELECT c.* FROM remote_deployment_claims c
JOIN deployments d ON d.id = c.deployment_id
WHERE c.agent_id = sqlc.arg(agent_id) AND c.state = 'waiting'
  AND d.assigned_agent_id = c.agent_id AND d.status = 'pending'
ORDER BY c.created_at, c.deployment_id;

-- name: ClaimRemoteDeployment :execrows
UPDATE remote_deployment_claims SET state = 'claimed',
    claim_token_hash = sqlc.arg(claim_token_hash),
    ciphertext = sqlc.arg(ciphertext),
    claim_expires_at = CAST(sqlc.arg(claim_expires_at) AS BIGINT),
    last_heartbeat_at = CAST(sqlc.arg(now) AS BIGINT),
    updated_at = CAST(sqlc.arg(now) AS BIGINT)
WHERE remote_deployment_claims.deployment_id = sqlc.arg(deployment_id)
  AND remote_deployment_claims.agent_id = sqlc.arg(agent_id)
  AND remote_deployment_claims.state = 'waiting'
  AND remote_deployment_claims.claim_token_hash IS NULL
  AND remote_deployment_claims.started_at IS NULL
  AND CAST(sqlc.arg(claim_expires_at) AS BIGINT) >
      CAST(sqlc.arg(now) AS BIGINT)
  AND EXISTS (SELECT 1 FROM deployments d
      WHERE d.id = remote_deployment_claims.deployment_id
        AND d.assigned_agent_id = remote_deployment_claims.agent_id
        AND d.status = 'pending')
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = remote_deployment_claims.agent_id AND a.status = 'active'
        AND EXISTS (SELECT 1 FROM agent_pairings p
            WHERE p.agent_id = a.id AND p.state = 'paired'))
  AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims busy
      WHERE busy.agent_id = remote_deployment_claims.agent_id
        AND busy.deployment_id <> remote_deployment_claims.deployment_id
        AND busy.state IN ('claimed', 'started', 'cancel_requested'));

-- name: ExpireRemoteClaims :execrows
UPDATE remote_deployment_claims SET state = 'waiting', reason = NULL,
    claim_token_hash = NULL, ciphertext = NULL, claim_expires_at = NULL,
    last_heartbeat_at = NULL, updated_at = sqlc.arg(now)
WHERE state = 'claimed' AND started_at IS NULL
  AND claim_expires_at <= sqlc.arg(now);

-- name: StartRemoteDeployment :execrows
UPDATE remote_deployment_claims SET state = 'started',
    started_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE remote_deployment_claims.deployment_id = sqlc.arg(deployment_id)
  AND remote_deployment_claims.agent_id = sqlc.arg(agent_id)
  AND remote_deployment_claims.claim_token_hash = sqlc.arg(claim_token_hash)
  AND remote_deployment_claims.state = 'claimed'
  AND remote_deployment_claims.started_at IS NULL
  AND remote_deployment_claims.claim_expires_at > sqlc.arg(now)
  AND EXISTS (SELECT 1 FROM deployments d
      WHERE d.id = remote_deployment_claims.deployment_id
        AND d.assigned_agent_id = remote_deployment_claims.agent_id
        AND d.status = 'pending')
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = remote_deployment_claims.agent_id AND a.status = 'active'
        AND EXISTS (SELECT 1 FROM agent_pairings p
            WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: HeartbeatRemoteDeployment :execrows
UPDATE remote_deployment_claims SET last_heartbeat_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE remote_deployment_claims.deployment_id = sqlc.arg(deployment_id)
  AND remote_deployment_claims.agent_id = sqlc.arg(agent_id)
  AND remote_deployment_claims.claim_token_hash = sqlc.arg(claim_token_hash)
  AND remote_deployment_claims.state IN ('started', 'cancel_requested')
  AND remote_deployment_claims.last_heartbeat_at <= sqlc.arg(now)
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = remote_deployment_claims.agent_id AND a.status = 'active'
        AND EXISTS (SELECT 1 FROM agent_pairings p
            WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: FinishRemoteDeployment :execrows
UPDATE remote_deployment_claims SET state = sqlc.arg(state),
    reason = sqlc.narg(reason), finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE remote_deployment_claims.deployment_id = sqlc.arg(deployment_id)
  AND remote_deployment_claims.agent_id = sqlc.arg(agent_id)
  AND remote_deployment_claims.claim_token_hash = sqlc.arg(claim_token_hash)
  AND remote_deployment_claims.state = 'started'
  AND remote_deployment_claims.started_at IS NOT NULL
  AND (sqlc.arg(state) = 'succeeded' OR sqlc.arg(state) = 'failed')
  AND EXISTS (SELECT 1 FROM deployments d
      WHERE d.id = remote_deployment_claims.deployment_id
        AND d.assigned_agent_id = remote_deployment_claims.agent_id
        AND d.status IN ('pending', 'running'))
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = remote_deployment_claims.agent_id AND a.status = 'active'
        AND EXISTS (SELECT 1 FROM agent_pairings p
            WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: LockRemoteDeploymentClaim :execrows
UPDATE remote_deployment_claims SET updated_at = updated_at
WHERE remote_deployment_claims.deployment_id = sqlc.arg(deployment_id)
  AND remote_deployment_claims.agent_id = sqlc.arg(agent_id)
  AND remote_deployment_claims.claim_token_hash = sqlc.arg(claim_token_hash)
  AND remote_deployment_claims.state IN ('started', 'cancel_requested')
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = remote_deployment_claims.agent_id AND a.status = 'active'
        AND EXISTS (SELECT 1 FROM agent_pairings p
            WHERE p.agent_id = a.id AND p.state = 'paired'));

-- name: RequestRemoteDeploymentCancellation :execrows
UPDATE remote_deployment_claims SET
    state = CASE WHEN started_at IS NULL THEN 'cancelled'
        ELSE 'cancel_requested' END,
    finished_at = CASE WHEN started_at IS NULL THEN sqlc.arg(now) ELSE NULL END,
    cancel_requested_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND state IN ('waiting', 'claimed', 'started');

-- name: AcknowledgeRemoteDeploymentCancellation :execrows
UPDATE remote_deployment_claims SET state = 'cancelled',
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND state = 'cancel_requested';

-- name: ExpireRemoteCancellation :execrows
UPDATE remote_deployment_claims SET state = 'cancel_unconfirmed',
    reason = 'cancel_ack_timeout', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE state = 'cancel_requested'
  AND cancel_requested_at <= sqlc.arg(stale_before);

-- name: LoseStaleRemoteClaims :execrows
UPDATE remote_deployment_claims SET state = 'lost',
    reason = 'heartbeat_lost', finished_at = sqlc.arg(now),
    updated_at = sqlc.arg(now)
WHERE state = 'started'
  AND last_heartbeat_at <= sqlc.arg(stale_before);
