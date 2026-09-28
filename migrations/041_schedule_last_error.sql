-- +goose Up
-- +goose StatementBegin
ALTER TABLE scheduled_deployments ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE runbook_schedules ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE schedule_last_error_rollback_refused (
    guard INTEGER CONSTRAINT schedule_last_error_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO schedule_last_error_rollback_refused VALUES (1);
-- +goose StatementEnd
