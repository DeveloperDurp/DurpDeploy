-- +goose Up
CREATE TABLE agent_execution_modes (
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    execution_mode NVARCHAR(16) NOT NULL CHECK (execution_mode IN ('host', 'container')),
    PRIMARY KEY (agent_id, execution_mode)
);
CREATE TABLE agent_container_runtimes (
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    runtime NVARCHAR(16) NOT NULL CHECK (runtime IN ('docker', 'podman')),
    PRIMARY KEY (agent_id, runtime)
);
CREATE TABLE agent_container_interpreters (
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    interpreter NVARCHAR(16) NOT NULL CHECK (interpreter IN ('bash', 'pwsh', 'python3')),
    PRIMARY KEY (agent_id, interpreter)
);
INSERT INTO agent_execution_modes (agent_id, execution_mode)
SELECT DISTINCT agent_id, 'host' FROM agent_interpreters;

-- +goose Down
CREATE TABLE agent_execution_capabilities_rollback_refused (
    guard BIGINT CHECK (guard = 0)
);
INSERT INTO agent_execution_capabilities_rollback_refused VALUES (1);
