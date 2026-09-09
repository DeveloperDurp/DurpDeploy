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
