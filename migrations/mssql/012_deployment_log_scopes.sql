-- +goose Up
-- A companion key preserves existing log IDs and the old INSERT contract.
CREATE UNIQUE INDEX idx_deployment_logs_scope_owner
    ON deployment_logs(deployment_id, id);
CREATE TABLE deployment_log_scopes (
    log_id BIGINT PRIMARY KEY,
    deployment_id BIGINT NOT NULL,
    step_index BIGINT,
    attempt BIGINT,
    sequence BIGINT NOT NULL CHECK (sequence >= 0),
    FOREIGN KEY (deployment_id, log_id)
        REFERENCES deployment_logs(deployment_id, id) ON DELETE NO ACTION,
    FOREIGN KEY (deployment_id, step_index, attempt)
        REFERENCES deployment_step_attempts(deployment_id, step_index, attempt) ON DELETE NO ACTION,
    CHECK ((step_index IS NULL AND attempt IS NULL)
        OR (step_index IS NOT NULL AND step_index >= 0 AND attempt IS NOT NULL AND attempt > 0))
);
CREATE UNIQUE INDEX idx_deployment_log_scopes_attempt_sequence
    ON deployment_log_scopes(deployment_id, step_index, attempt, sequence)
    WHERE step_index IS NOT NULL;
CREATE UNIQUE INDEX idx_deployment_log_scopes_legacy_sequence
    ON deployment_log_scopes(deployment_id, sequence) WHERE step_index IS NULL;
INSERT INTO deployment_log_scopes (log_id, deployment_id, sequence)
    SELECT id, deployment_id, id FROM deployment_logs;

-- +goose Down
-- Removing these identities would allow duplicate replay of acknowledged logs.
CREATE TABLE deployment_logs_rollback_refused (
    guard BIGINT CONSTRAINT deployment_log_history_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO deployment_logs_rollback_refused VALUES (1);
