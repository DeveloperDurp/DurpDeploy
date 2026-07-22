-- +goose Up
-- +goose StatementBegin

-- global_notifications is a singleton table (always id=1) holding the
-- notification channels used for project-less/system-wide events (e.g.
-- litestream backup health). Mirrors the per-project columns on
-- `projects` (015_notifications.sql, 016_gotify.sql, 017_discord.sql) so
-- events.Bus can load either a project's settings or these global ones
-- with the same shape.
CREATE TABLE IF NOT EXISTS global_notifications (
    id INTEGER PRIMARY KEY,
    slack_webhook_url TEXT,
    notify_emails TEXT,
    gotify_url TEXT,
    gotify_token TEXT,
    discord_webhook_url TEXT,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

INSERT INTO global_notifications (id) VALUES (1);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS global_notifications;

-- +goose StatementEnd
