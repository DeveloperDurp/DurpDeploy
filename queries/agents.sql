-- name: CreatePendingAgent :one
INSERT INTO agents (id, name, agent_version)
VALUES (?, ?, ?)
RETURNING id, name, status, agent_version, created_at;

-- name: GetAgent :one
SELECT * FROM agents WHERE id = ?;

-- name: ListAgents :many
SELECT * FROM agents ORDER BY name ASC, id ASC;

-- name: DeletePendingUnreferencedAgent :execrows
DELETE FROM agents
WHERE id = ?
  AND status = 'pending'
  AND NOT EXISTS (
      SELECT 1 FROM agent_pool_memberships WHERE agent_id = agents.id
  )
  AND NOT EXISTS (SELECT 1 FROM agent_tags WHERE agent_id = agents.id)
  AND NOT EXISTS (
      SELECT 1 FROM agent_enrollment_tokens WHERE agent_id = agents.id
  )
  AND NOT EXISTS (SELECT 1 FROM agent_events WHERE agent_id = agents.id)
  AND NOT EXISTS (
      SELECT 1 FROM deployment_dispatches WHERE agent_id = agents.id
  );

-- name: CreateAgentEnrollmentToken :exec
INSERT INTO agent_enrollment_tokens (
    token_hash, token_prefix, agent_id, expires_at
) VALUES (?, ?, ?, ?);

-- name: ConsumeAgentEnrollmentToken :execrows
UPDATE agent_enrollment_tokens
SET used_at = ?
WHERE token_hash = ?
  AND agent_id = ?
  AND expires_at > ?
  AND used_at IS NULL
  AND revoked_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM agents
      WHERE agents.id = agent_enrollment_tokens.agent_id
        AND agents.status = 'pending'
  );

-- name: ActivatePendingAgent :one
UPDATE agents
SET status = 'active',
    certificate_pem = ?,
    certificate_fingerprint = ?,
    agent_version = ?,
    last_heartbeat_at = ?,
    enrolled_at = ?,
    updated_at = ?
WHERE id = ?
  AND status = 'pending'
RETURNING id, status, certificate_fingerprint, agent_version, enrolled_at;

-- name: GetActiveAgentByFingerprint :one
SELECT id, name, agent_version, certificate_pem, certificate_fingerprint, last_heartbeat_at,
       enrolled_at
FROM agents
WHERE certificate_fingerprint = ?
  AND status = 'active';

-- name: UpdateAgentHeartbeat :execrows
UPDATE agents
SET agent_version = ?,
    last_heartbeat_at = ?,
    updated_at = ?
WHERE id = ?
  AND status = 'active';

-- name: TouchAgentHeartbeat :execrows
UPDATE agents
SET last_heartbeat_at = sqlc.arg(last_heartbeat_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status = 'active';

-- name: DisableAgent :execrows
UPDATE agents
SET status = 'disabled',
    updated_at = ?
WHERE id = ?
  AND status = 'active';

-- name: RevokeAgent :execrows
UPDATE agents
SET status = 'revoked',
    revoked_at = ?,
    updated_at = ?
WHERE id = ?
  AND status IN ('active', 'disabled');

-- name: ReenrollAgent :execrows
UPDATE agents
SET status = 'pending',
    certificate_pem = NULL,
    certificate_fingerprint = NULL,
    last_heartbeat_at = NULL,
    enrolled_at = NULL,
    revoked_at = NULL,
    updated_at = ?
WHERE id = ?
  AND status = 'revoked';

-- name: RevokeAgentEnrollmentTokens :exec
UPDATE agent_enrollment_tokens
SET revoked_at = ?
WHERE agent_id = ?
  AND used_at IS NULL
  AND revoked_at IS NULL;

-- name: ListRedactedAgentEventsByAgent :many
SELECT id, agent_id, deployment_id, event_type, dispatch_state, created_at
FROM agent_events
WHERE agent_id = ?
ORDER BY created_at DESC, id DESC;
