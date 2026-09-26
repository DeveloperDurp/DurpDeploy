-- +goose Up
CREATE INDEX IF NOT EXISTS idx_runbook_executions_schedule
    ON runbook_executions(schedule_id);

-- +goose Down
DROP INDEX IF EXISTS idx_runbook_executions_schedule;
