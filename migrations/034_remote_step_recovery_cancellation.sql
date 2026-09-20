-- +goose Up
ALTER TABLE remote_step_runs ADD COLUMN recovery_cancelled INTEGER NOT NULL
    DEFAULT 0 CHECK (recovery_cancelled IN (0, 1));

-- +goose Down
CREATE TABLE remote_step_recovery_cancellation_rollback_refused (
    guard INTEGER CONSTRAINT remote_step_recovery_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_step_recovery_cancellation_rollback_refused VALUES (1);
