-- +goose Up
ALTER TABLE remote_deployment_claims ADD log_buffer_ciphertext NVARCHAR(MAX) NULL;
ALTER TABLE remote_step_runs ADD log_buffer_ciphertext NVARCHAR(MAX) NULL;

-- +goose Down
CREATE TABLE remote_log_buffers_rollback_refused (
    guard BIGINT CONSTRAINT remote_log_buffers_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_log_buffers_rollback_refused VALUES (1);
