-- +goose Up
-- +goose StatementBegin
IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE object_id = OBJECT_ID('runbook_executions')
      AND name = 'idx_runbook_executions_schedule'
)
    CREATE INDEX idx_runbook_executions_schedule
        ON runbook_executions(schedule_id);
-- +goose StatementEnd

-- +goose Down
CREATE TABLE runbook_schedule_index_rollback_refused (
    guard BIGINT CONSTRAINT runbook_schedule_index_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO runbook_schedule_index_rollback_refused VALUES (1);
