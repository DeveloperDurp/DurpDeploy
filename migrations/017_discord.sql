-- +goose Up
-- +goose StatementBegin

ALTER TABLE projects ADD COLUMN discord_webhook_url TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Note: SQLite <3.35 cannot DROP COLUMN. The added column remains after
-- Down, same as 003_lifecycles.sql / 015_notifications.sql / 016_gotify.sql.
-- Manual cleanup required for full rollback.

-- +goose StatementEnd
