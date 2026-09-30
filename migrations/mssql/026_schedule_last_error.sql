-- +goose Up
-- +goose StatementBegin
ALTER TABLE scheduled_deployments ADD last_error NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_scheduled_deployments_last_error DEFAULT '';
ALTER TABLE runbook_schedules ADD last_error NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_runbook_schedules_last_error DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE schedule_last_error_rollback_refused (
    guard BIGINT CONSTRAINT schedule_last_error_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO schedule_last_error_rollback_refused VALUES (1);
-- +goose StatementEnd
