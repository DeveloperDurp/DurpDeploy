-- name: AddAgentExecutionMode :exec
INSERT INTO agent_execution_modes (agent_id, execution_mode) VALUES (?, ?);

-- name: DeleteAgentExecutionModes :exec
DELETE FROM agent_execution_modes WHERE agent_id = ?;

-- name: ListAgentExecutionModes :many
SELECT execution_mode FROM agent_execution_modes
WHERE agent_id = ? ORDER BY execution_mode;

-- name: AddAgentContainerRuntime :exec
INSERT INTO agent_container_runtimes (agent_id, runtime) VALUES (?, ?);

-- name: DeleteAgentContainerRuntimes :exec
DELETE FROM agent_container_runtimes WHERE agent_id = ?;

-- name: ListAgentContainerRuntimes :many
SELECT runtime FROM agent_container_runtimes WHERE agent_id = ? ORDER BY runtime;

-- name: AddAgentContainerInterpreter :exec
INSERT INTO agent_container_interpreters (agent_id, interpreter) VALUES (?, ?);

-- name: DeleteAgentContainerInterpreters :exec
DELETE FROM agent_container_interpreters WHERE agent_id = ?;

-- name: ListAgentContainerInterpreters :many
SELECT interpreter FROM agent_container_interpreters
WHERE agent_id = ? ORDER BY interpreter;

-- name: ListAgentPollCapabilities :many
SELECT 'host' AS category, interpreter AS capability
FROM agent_interpreters WHERE agent_interpreters.agent_id = sqlc.arg(agent_id)
UNION ALL
SELECT 'mode', execution_mode FROM agent_execution_modes
WHERE agent_execution_modes.agent_id = sqlc.arg(agent_id)
UNION ALL
SELECT 'runtime', runtime FROM agent_container_runtimes
WHERE agent_container_runtimes.agent_id = sqlc.arg(agent_id)
UNION ALL
SELECT 'container', interpreter FROM agent_container_interpreters
WHERE agent_container_interpreters.agent_id = sqlc.arg(agent_id)
UNION ALL
SELECT 'staging', '' FROM agent_staging_capabilities
WHERE agent_staging_capabilities.agent_id = sqlc.arg(agent_id)
UNION ALL
SELECT 'artifact', '' FROM agent_artifact_capabilities
WHERE agent_artifact_capabilities.agent_id = sqlc.arg(agent_id)
UNION ALL
SELECT 'resource_policy', advertisement FROM agent_resource_policies
WHERE agent_resource_policies.agent_id = sqlc.arg(agent_id)
UNION ALL
SELECT 'protocol', COALESCE(agent_protocol, '') FROM agents
WHERE agents.id = sqlc.arg(agent_id)
ORDER BY category ASC, capability ASC;
