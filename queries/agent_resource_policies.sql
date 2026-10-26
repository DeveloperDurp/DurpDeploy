-- name: AddAgentResourcePolicy :exec
INSERT INTO agent_resource_policies (agent_id, advertisement) VALUES (?, ?);

-- name: ClearAgentResourcePolicy :exec
DELETE FROM agent_resource_policies WHERE agent_id = ?;

-- name: GetAgentResourcePolicy :one
SELECT advertisement FROM agent_resource_policies WHERE agent_id = ?;
