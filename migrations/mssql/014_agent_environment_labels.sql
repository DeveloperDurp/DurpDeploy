-- +goose Up
CREATE TABLE agent_environment_labels (
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    environment_id BIGINT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (agent_id, environment_id)
);
CREATE INDEX idx_agent_environment_labels_environment
    ON agent_environment_labels(environment_id, agent_id);

-- +goose Down
CREATE TABLE agent_environment_labels_rollback_refused (
    guard BIGINT CONSTRAINT agent_environment_labels_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO agent_environment_labels_rollback_refused VALUES (1);
