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
