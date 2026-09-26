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
DROP INDEX IF EXISTS idx_runbook_executions_schedule ON runbook_executions;
