-- name: CreateDeploymentPayload :one
INSERT INTO deployment_payloads (deployment_id, ciphertext)
VALUES (sqlc.arg(deployment_id), sqlc.arg(ciphertext))
ON CONFLICT(deployment_id) DO UPDATE
SET deployment_id = deployment_payloads.deployment_id
RETURNING *;

-- name: GetDeploymentPayload :one
SELECT * FROM deployment_payloads WHERE deployment_id = ?;

-- name: CreateDeploymentDispatch :one
INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, selector, state, reason)
VALUES (
    sqlc.arg(deployment_id),
    sqlc.arg(mode),
    sqlc.narg(pool_id),
    sqlc.arg(selector),
    sqlc.arg(state),
    sqlc.narg(reason)
)
ON CONFLICT(deployment_id) DO UPDATE
SET deployment_id = deployment_dispatches.deployment_id
RETURNING *;

-- name: GetDeploymentDispatch :one
SELECT * FROM deployment_dispatches WHERE deployment_id = ?;

-- name: ClaimDeploymentDispatch :one
UPDATE deployment_dispatches
SET state = 'claimed',
    agent_id = sqlc.arg(agent_id),
    claim_token_hash = sqlc.arg(claim_token_hash),
    claim_expires_at = sqlc.arg(claim_expires_at),
    last_heartbeat_at = sqlc.arg(last_heartbeat_at),
    updated_at = unixepoch()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND state = 'waiting'
  AND EXISTS (
      SELECT 1
      FROM agents AS a
      JOIN agent_pool_memberships AS m ON m.agent_id = a.id
      WHERE a.id = sqlc.arg(agent_id)
        AND a.status = 'active'
        AND m.pool_id = deployment_dispatches.pool_id
  )
  AND (
      selector = ''
      OR (
          SELECT COUNT(*)
          FROM agent_tags AS t
          WHERE t.agent_id = sqlc.arg(agent_id)
            AND instr(
                ',' || deployment_dispatches.selector || ',',
                ',' || t.tag_key || '=' || t.tag_value || ','
            ) > 0
      ) = length(deployment_dispatches.selector) -
          length(replace(deployment_dispatches.selector, ',', '')) + 1
  )
  AND NOT EXISTS (
      SELECT 1
      FROM deployment_dispatches AS active_dispatch
        WHERE active_dispatch.agent_id = ?1
        AND active_dispatch.state IN ('claimed', 'started', 'cancel_requested')
  )
RETURNING *;

-- name: ClaimOldestEligibleDeployment :one
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
    JOIN agent_pools AS pool ON pool.id = candidate.pool_id
    JOIN agent_pool_memberships AS membership
        ON membership.pool_id = candidate.pool_id
    JOIN agents AS agent ON agent.id = membership.agent_id
    WHERE candidate.mode = 'remote'
      AND candidate.state = 'waiting'
      AND pool.enabled = 1
      AND agent.id = sqlc.arg(agent_id)
      AND agent.status = 'active'
      AND (
          candidate.selector = ''
          OR (
              SELECT COUNT(*)
              FROM agent_tags AS tag
              WHERE tag.agent_id = agent.id
                AND instr(
                    ',' || candidate.selector || ',',
                    ',' || tag.tag_key || '=' || tag.tag_value || ','
                ) > 0
          ) = length(candidate.selector) -
              length(replace(candidate.selector, ',', '')) + 1
      )
      AND NOT EXISTS (
          SELECT 1
          FROM deployment_dispatches AS active_dispatch
          WHERE active_dispatch.agent_id = agent.id
            AND active_dispatch.state IN ('claimed', 'started', 'cancel_requested')
      )
    ORDER BY candidate.created_at ASC, candidate.deployment_id ASC
    LIMIT 1
)
RETURNING *;

-- name: StartClaimedDeploymentDispatch :one
UPDATE deployment_dispatches
SET state = 'started',
    started_at = sqlc.arg(started_at),
    claim_expires_at = NULL,
    last_heartbeat_at = sqlc.arg(last_heartbeat_at),
    updated_at = unixepoch()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND state = 'claimed'
RETURNING *;

-- name: RenewDeploymentDispatchClaim :one
UPDATE deployment_dispatches
SET claim_expires_at = sqlc.arg(claim_expires_at),
    last_heartbeat_at = sqlc.arg(last_heartbeat_at),
    updated_at = unixepoch()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND state = sqlc.arg(current_state)
RETURNING *;

-- name: TransitionDeploymentDispatch :one
UPDATE deployment_dispatches
SET state = sqlc.arg(next_state),
    reason = sqlc.narg(reason),
    finished_at = sqlc.narg(finished_at),
    updated_at = unixepoch()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
   AND state = sqlc.arg(current_state)
RETURNING *;

-- name: RequestDeploymentDispatchCancellation :one
UPDATE deployment_dispatches
SET state = 'cancel_requested',
    cancel_requested_at = sqlc.arg(cancel_requested_at),
    updated_at = unixepoch()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND state = sqlc.arg(current_state)
RETURNING *;

-- name: AcknowledgeDeploymentDispatchCancellation :one
UPDATE deployment_dispatches
SET state = 'cancelled',
    finished_at = sqlc.arg(finished_at),
    updated_at = unixepoch()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND state = 'cancel_requested'
RETURNING *;

-- name: ExpireDeploymentDispatchCancellation :execrows
UPDATE deployment_dispatches
SET state = 'cancel_unconfirmed',
    updated_at = unixepoch()
WHERE state = 'cancel_requested'
  AND cancel_requested_at <= sqlc.arg(cancel_requested_before);

-- name: ListExpiredClaimedDeploymentDispatches :many
SELECT *
FROM deployment_dispatches
WHERE state = 'claimed'
  AND claim_expires_at <= sqlc.arg(claim_expires_before);

-- name: ReclaimExpiredClaimedDeploymentDispatch :one
UPDATE deployment_dispatches
SET state = 'waiting',
    agent_id = NULL,
    claim_token_hash = NULL,
    claim_expires_at = NULL,
    last_heartbeat_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash)
  AND state = 'claimed'
  AND claim_expires_at <= sqlc.arg(claim_expires_before)
RETURNING *;

-- name: ListLostStartedDeploymentDispatches :many
SELECT *
FROM deployment_dispatches
WHERE state = 'started'
  AND last_heartbeat_at <= sqlc.arg(last_heartbeat_before);

-- name: ListExpiredCancellationDeploymentDispatches :many
SELECT *
FROM deployment_dispatches
WHERE state = 'cancel_requested'
  AND cancel_requested_at <= sqlc.arg(cancel_requested_before);

-- name: CreateAgentEvent :one
INSERT INTO agent_events (agent_id, deployment_id, event_type, dispatch_state)
VALUES (
    sqlc.narg(agent_id),
    sqlc.narg(deployment_id),
    sqlc.arg(event_type),
    sqlc.narg(dispatch_state)
)
RETURNING *;

-- name: CreateOfflineAgentEvent :execrows
INSERT INTO agent_events (agent_id, event_type)
SELECT a.id, 'agent_offline'
FROM agents AS a
WHERE a.id = sqlc.arg(agent_id)
  AND a.status = 'active'
  AND a.last_heartbeat_at <= sqlc.arg(last_heartbeat_before)
  AND NOT EXISTS (
      SELECT 1
      FROM agent_events AS event
      WHERE event.agent_id = a.id
        AND event.event_type = 'agent_offline'
        AND event.created_at >= a.last_heartbeat_at
  );

-- name: ListOfflineAgentIDs :many
SELECT id
FROM agents
WHERE status = 'active'
  AND last_heartbeat_at <= sqlc.arg(last_heartbeat_before);

-- name: CreateAgentEventForDispatch :one
INSERT INTO agent_events (agent_id, deployment_id, event_type, dispatch_state, details)
SELECT
    sqlc.arg(agent_id),
    sqlc.arg(deployment_id),
    sqlc.arg(event_type),
    sqlc.arg(current_state),
    sqlc.narg(details)
WHERE EXISTS (
    SELECT 1
    FROM deployment_dispatches
    WHERE deployment_id = sqlc.arg(deployment_id)
      AND agent_id = sqlc.arg(agent_id)
      AND claim_token_hash = sqlc.arg(claim_token_hash)
      AND state = sqlc.arg(current_state)
)
RETURNING *;
