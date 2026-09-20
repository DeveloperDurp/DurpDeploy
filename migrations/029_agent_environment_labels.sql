-- +goose Up
CREATE TABLE agent_environment_labels (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (agent_id, environment_id)
);
CREATE INDEX idx_agent_environment_labels_environment
    ON agent_environment_labels(environment_id, agent_id);

-- +goose Down
CREATE TABLE agent_environment_labels_rollback_refused (
    guard INTEGER CONSTRAINT agent_environment_labels_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO agent_environment_labels_rollback_refused VALUES (1);
