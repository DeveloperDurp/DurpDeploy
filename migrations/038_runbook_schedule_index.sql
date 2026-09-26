-- +goose Up
CREATE INDEX IF NOT EXISTS idx_runbook_executions_schedule
    ON runbook_executions(schedule_id);

-- +goose Down
CREATE TABLE runbook_schedule_index_rollback_refused (
    guard INTEGER CONSTRAINT runbook_schedule_index_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO runbook_schedule_index_rollback_refused VALUES (1);
