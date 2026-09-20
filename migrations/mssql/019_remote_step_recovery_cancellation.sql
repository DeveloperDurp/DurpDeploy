-- +goose Up
ALTER TABLE remote_step_runs ADD recovery_cancelled BIGINT NOT NULL
    CONSTRAINT df_remote_step_runs_recovery_cancelled DEFAULT 0;
ALTER TABLE remote_step_runs ADD
    CONSTRAINT ck_remote_step_runs_recovery_cancelled
    CHECK (recovery_cancelled IN (0, 1));

-- +goose Down
CREATE TABLE remote_step_recovery_cancellation_rollback_refused (
    guard BIGINT CONSTRAINT remote_step_recovery_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_step_recovery_cancellation_rollback_refused VALUES (1);
