-- +goose Up
CREATE TABLE agent_interpreters (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    interpreter TEXT NOT NULL
        CHECK (interpreter IN ('bash', 'pwsh', 'python3')),
    PRIMARY KEY (agent_id, interpreter)
);
INSERT INTO agent_interpreters (agent_id, interpreter)
SELECT a.id, 'bash'
FROM agents a
JOIN agent_pairings p ON p.agent_id = a.id
WHERE a.status = 'active' AND a.revoked_at IS NULL
  AND p.state = 'paired';
CREATE INDEX idx_agent_interpreters_interpreter
    ON agent_interpreters(interpreter, agent_id);

-- +goose Down
CREATE TABLE agent_interpreters_rollback_refused (
    guard INTEGER CONSTRAINT agent_interpreters_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO agent_interpreters_rollback_refused VALUES (1);
