-- name: CreateAgent :one
INSERT INTO agents (id, name, endpoint) VALUES (?, ?, ?) RETURNING *;

-- name: GetAgent :one
SELECT * FROM agents WHERE id = ?;

-- name: ListAgents :many
SELECT * FROM agents ORDER BY name, id;

-- name: UpdateAgent :one
UPDATE agents SET name = ?, endpoint = ?, updated_at = unixepoch()
WHERE id = ? AND status IN ('pending', 'active', 'disabled') RETURNING *;

-- name: SetAgentStatus :one
UPDATE agents SET status = sqlc.arg(status), updated_at = unixepoch(),
    revoked_at = CASE WHEN sqlc.arg(status) = 'revoked' THEN unixepoch() ELSE revoked_at END
WHERE id = sqlc.arg(id) AND status IN ('pending', 'active', 'disabled')
  AND (sqlc.arg(status) = 'revoked'
       OR (status IN ('active', 'disabled') AND (sqlc.arg(status) = 'active' OR sqlc.arg(status) = 'disabled'))) RETURNING *;

-- name: ResetRevokedAgentForPairing :execrows
UPDATE agents SET endpoint = sqlc.arg(endpoint), status = 'pending',
    agent_version = NULL, certificate_pem = NULL,
    certificate_fingerprint = NULL, encrypted_identity = NULL,
    last_heartbeat_at = NULL, revoked_at = NULL, updated_at = unixepoch()
WHERE id = sqlc.arg(id) AND status = 'revoked';

-- name: DeletePendingAgent :execrows
DELETE FROM agents WHERE id = ? AND status = 'pending'
AND NOT EXISTS (SELECT 1 FROM agent_pairings WHERE agent_id = agents.id);

-- name: HeartbeatAgent :execrows
UPDATE agents SET last_heartbeat_at = sqlc.arg(now), agent_version = sqlc.narg(agent_version),
    updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND certificate_fingerprint = sqlc.arg(certificate_fingerprint)
  AND status = 'active'
  AND (last_heartbeat_at IS NULL OR last_heartbeat_at <= sqlc.arg(now))
  AND EXISTS (SELECT 1 FROM agent_pairings WHERE agent_id = agents.id AND state = 'paired');

-- name: AddAgentLabel :execrows
INSERT INTO agent_labels (agent_id, label)
SELECT sqlc.arg(agent_id), sqlc.arg(label)
WHERE NOT EXISTS (SELECT 1 FROM agent_labels WHERE agent_id = sqlc.arg(agent_id) AND label = sqlc.arg(label));

-- name: DeleteAgentLabel :execrows
DELETE FROM agent_labels WHERE agent_id = ? AND label = ?;

-- name: ListAgentLabels :many
SELECT label FROM agent_labels WHERE agent_id = ? ORDER BY label;

-- name: ListAvailableAgentLabels :many
SELECT DISTINCT label FROM agent_labels ORDER BY label;

-- name: AddAgentEnvironmentLabel :execrows
INSERT INTO agent_environment_labels (agent_id, environment_id)
SELECT sqlc.arg(agent_id), sqlc.arg(environment_id)
WHERE NOT EXISTS (SELECT 1 FROM agent_environment_labels
 WHERE agent_id = sqlc.arg(agent_id)
   AND environment_id = sqlc.arg(environment_id));

-- name: DeleteAgentEnvironmentLabel :execrows
DELETE FROM agent_environment_labels
WHERE agent_id = ? AND environment_id = ?;

-- name: ListAgentEnvironmentLabels :many
SELECT e.* FROM environments e
JOIN agent_environment_labels l ON l.environment_id = e.id
WHERE l.agent_id = ? ORDER BY e.name, e.id;

-- name: ListAvailableAgentEnvironmentLabels :many
SELECT e.* FROM environments e
WHERE NOT EXISTS (SELECT 1 FROM agent_environment_labels l
 WHERE l.agent_id = sqlc.arg(agent_id) AND l.environment_id = e.id)
ORDER BY e.name, e.id;

-- name: AssignEnvironmentAgent :execrows
INSERT INTO environment_agent_assignments (environment_id, agent_id)
SELECT sqlc.arg(environment_id), sqlc.arg(agent_id)
WHERE NOT EXISTS (SELECT 1 FROM environment_agent_assignments
 WHERE environment_id = sqlc.arg(environment_id));

-- name: UnassignEnvironmentAgent :execrows
DELETE FROM environment_agent_assignments WHERE environment_id = ? AND agent_id = ?;

-- name: ListEnvironmentAgents :many
SELECT a.* FROM agents a JOIN environment_agent_assignments e ON e.agent_id = a.id
WHERE e.environment_id = ? ORDER BY a.name, a.id;

-- name: GetEnvironmentAgentAssignment :one
SELECT * FROM environment_agent_assignments WHERE environment_id = ?;

-- name: LockEnvironmentAgentAssignment :execrows
UPDATE environment_agent_assignments SET created_at = created_at
WHERE environment_agent_assignments.environment_id = sqlc.arg(environment_id)
  AND environment_agent_assignments.agent_id = sqlc.arg(agent_id)
  AND EXISTS (SELECT 1 FROM agents a
      WHERE a.id = environment_agent_assignments.agent_id
        AND a.status = 'active')
  AND EXISTS (SELECT 1 FROM agent_pairings p
      WHERE p.agent_id = environment_agent_assignments.agent_id
        AND p.state = 'paired');

-- name: ListAgentEnvironments :many
SELECT e.* FROM environments e JOIN environment_agent_assignments a ON a.environment_id = e.id
WHERE a.agent_id = ? ORDER BY e.name, e.id;

-- name: ListAgentAssignments :many
SELECT * FROM environment_agent_assignments
WHERE agent_id = ? ORDER BY environment_id;

-- name: ListRevocableAgentClaims :many
SELECT * FROM remote_deployment_claims
WHERE agent_id = ? AND state IN ('waiting', 'claimed', 'started', 'cancel_requested')
ORDER BY deployment_id;

-- name: RevokeUnstartedRemoteClaim :execrows
UPDATE remote_deployment_claims SET state = 'failed',
    reason = 'remote_agent_revoked_before_start',
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND state IN ('waiting', 'claimed') AND started_at IS NULL;

-- name: RevokeStartedRemoteClaim :execrows
UPDATE remote_deployment_claims SET state = 'lost',
    reason = 'remote_agent_revoked_after_start', cancel_requested_at = NULL,
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND state IN ('started', 'cancel_requested') AND started_at IS NOT NULL;

-- name: TerminateRevokedRemoteDeployment :execrows
UPDATE deployments SET
    status = 'failed',
    finished_at = sqlc.arg(now)
WHERE id = sqlc.arg(deployment_id)
  AND assigned_agent_id = sqlc.arg(agent_id)
  AND status IN ('pending', 'pending_approval', 'running');
