-- +goose Up
ALTER TABLE remote_deployment_claims ADD COLUMN log_buffer_ciphertext TEXT;
ALTER TABLE remote_step_runs ADD COLUMN log_buffer_ciphertext TEXT;

-- +goose Down
CREATE TABLE remote_log_buffers_rollback_refused (
    guard INTEGER CONSTRAINT remote_log_buffers_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_log_buffers_rollback_refused VALUES (1);
