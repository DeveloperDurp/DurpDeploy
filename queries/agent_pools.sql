-- name: ListAgentPools :many
SELECT * FROM agent_pools ORDER BY name ASC;

-- name: GetAgentPool :one
SELECT * FROM agent_pools WHERE id = ?;

-- name: CreateAgentPool :one
INSERT INTO agent_pools (name, description) VALUES (?, ?) RETURNING *;

-- name: UpdateAgentPool :one
UPDATE agent_pools
SET name = ?, description = ?, enabled = ?
WHERE id = ?
RETURNING *;

-- name: DisableAgentPool :exec
UPDATE agent_pools SET enabled = 0 WHERE id = ?;

-- name: DeleteAgentPool :exec
DELETE FROM agent_pools WHERE id = ?;

-- name: AddAgentToPool :exec
INSERT INTO agent_pool_memberships (pool_id, agent_id) VALUES (?, ?)
ON CONFLICT (pool_id, agent_id) DO NOTHING;

-- name: CreateAgentPoolMembership :exec
INSERT INTO agent_pool_memberships (pool_id, agent_id) VALUES (?, ?);

-- name: RemoveAgentFromPool :exec
DELETE FROM agent_pool_memberships WHERE pool_id = ? AND agent_id = ?;

-- name: ListAgentPoolMembers :many
SELECT a.*
FROM agents a
JOIN agent_pool_memberships apm ON apm.agent_id = a.id
WHERE apm.pool_id = ?
ORDER BY a.name ASC, a.id ASC;

-- name: ListAgentPoolsByAgent :many
SELECT ap.*
FROM agent_pools ap
JOIN agent_pool_memberships apm ON apm.pool_id = ap.id
WHERE apm.agent_id = ?
ORDER BY ap.name ASC;

-- name: SetAgentTag :exec
INSERT INTO agent_tags (agent_id, tag_key, tag_value) VALUES (?, ?, ?)
ON CONFLICT (agent_id, tag_key) DO UPDATE SET tag_value = excluded.tag_value;

-- name: DeleteAgentTag :exec
DELETE FROM agent_tags WHERE agent_id = ? AND tag_key = ?;

-- name: ListAgentTagsByAgent :many
SELECT tag_key, tag_value, created_at
FROM agent_tags
WHERE agent_id = ?
ORDER BY tag_key ASC;

-- name: GetEnvironmentAgentPolicy :one
SELECT * FROM environment_agent_policies WHERE environment_id = ?;

-- name: ListEnvironmentAgentPolicies :many
SELECT * FROM environment_agent_policies ORDER BY environment_id ASC;

-- name: UpsertEnvironmentAgentPolicy :exec
INSERT INTO environment_agent_policies (environment_id, pool_id, selector)
VALUES (?, ?, ?)
ON CONFLICT (environment_id) DO UPDATE SET
    pool_id = excluded.pool_id,
    selector = excluded.selector;

-- name: DeleteEnvironmentAgentPolicy :exec
DELETE FROM environment_agent_policies WHERE environment_id = ?;

-- name: ListAgentPoolCandidatesByEnvironment :many
SELECT a.*
FROM environment_agent_policies eap
JOIN agent_pools ap ON ap.id = eap.pool_id
JOIN agent_pool_memberships apm ON apm.pool_id = ap.id
JOIN agents a ON a.id = apm.agent_id
WHERE eap.environment_id = ?
  AND ap.enabled = 1
  AND a.status = 'active'
ORDER BY a.id ASC;
