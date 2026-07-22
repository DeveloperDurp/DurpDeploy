-- +goose Up
-- +goose StatementBegin

ALTER TABLE projects ADD COLUMN gotify_url TEXT;
ALTER TABLE projects ADD COLUMN gotify_token TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Note: SQLite <3.35 cannot DROP COLUMN. The added columns remain after
-- Down, same as 003_lifecycles.sql / 015_notifications.sql. Manual cleanup
-- required for full rollback.

-- +goose StatementEnd
