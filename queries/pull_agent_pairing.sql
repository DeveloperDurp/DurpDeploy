-- name: CreateAgentPairing :one
INSERT INTO agent_pairings (
    agent_id,
    pairing_code_hash,
    agent_public_identity,
    agent_pin,
    state,
    expires_at
) VALUES (?, ?, ?, ?, 'pending', ?)
ON CONFLICT(agent_id) DO UPDATE
SET pairing_code_hash = excluded.pairing_code_hash,
    agent_public_identity = excluded.agent_public_identity,
    agent_pin = excluded.agent_pin,
    server_public_identity = NULL,
    server_pin = NULL,
    state = 'pending',
    expires_at = excluded.expires_at,
    paired_at = NULL,
    updated_at = unixepoch()
RETURNING *;

-- name: GetAgentPairing :one
SELECT * FROM agent_pairings WHERE agent_id = ?;

-- name: CompleteAgentPairing :one
UPDATE agent_pairings
SET state = 'paired',
    server_public_identity = sqlc.arg(server_public_identity),
    server_pin = sqlc.arg(server_pin),
    paired_at = sqlc.arg(paired_at),
    updated_at = sqlc.arg(updated_at)
WHERE agent_id = sqlc.arg(agent_id)
  AND pairing_code_hash = sqlc.arg(pairing_code_hash)
  AND agent_pin = sqlc.arg(agent_pin)
  AND state = 'pending'
  AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: ExpirePendingAgentPairings :execrows
UPDATE agent_pairings
SET state = 'expired', updated_at = sqlc.arg(updated_at)
WHERE state = 'pending'
  AND expires_at <= sqlc.arg(expires_before);

-- name: GetEnvironmentAgentAssignment :one
SELECT * FROM environment_agent_assignments WHERE environment_id = ?;

-- name: AssignAgentToEnvironment :one
INSERT INTO environment_agent_assignments (
    environment_id, agent_id, updated_at
) VALUES (?, ?, ?)
ON CONFLICT(environment_id) DO UPDATE
SET agent_id = excluded.agent_id,
    updated_at = excluded.updated_at
RETURNING *;

-- name: DeleteEnvironmentAgentAssignment :execrows
DELETE FROM environment_agent_assignments WHERE environment_id = ?;

-- name: ListEnvironmentAgentAssignmentsByAgent :many
SELECT * FROM environment_agent_assignments
WHERE agent_id = ?
ORDER BY environment_id ASC;

-- name: CreateDirectDeploymentDispatch :one
INSERT INTO deployment_dispatches (
    deployment_id, mode, state, assigned_agent_id
) VALUES (?, 'remote', 'waiting', ?)
RETURNING *;

-- name: ClaimOldestDirectDeployment :one
UPDATE deployment_dispatches
SET state = 'claimed',
    agent_id = sqlc.arg(agent_id),
    claim_token_hash = sqlc.arg(claim_token_hash),
    claim_expires_at = sqlc.arg(claim_expires_at),
    last_heartbeat_at = sqlc.arg(last_heartbeat_at),
    updated_at = unixepoch()
WHERE deployment_id = (
    SELECT candidate.deployment_id
    FROM deployment_dispatches AS candidate
    JOIN agents AS agent ON agent.id = candidate.assigned_agent_id
    JOIN agent_pairings AS pairing ON pairing.agent_id = agent.id
    WHERE candidate.mode = 'remote'
      AND candidate.state = 'waiting'
      AND candidate.assigned_agent_id = sqlc.arg(agent_id)
      AND agent.status = 'active'
      AND pairing.state = 'paired'
      AND NOT EXISTS (
          SELECT 1
          FROM deployment_dispatches AS active_dispatch
          WHERE active_dispatch.agent_id = ?1
            AND active_dispatch.state IN ('claimed', 'started', 'cancel_requested')
      )
    ORDER BY candidate.created_at ASC, candidate.deployment_id ASC
    LIMIT 1
)
AND assigned_agent_id = sqlc.arg(agent_id)
AND state = 'waiting'
RETURNING *;
