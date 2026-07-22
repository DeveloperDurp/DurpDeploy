-- +goose Up
-- +goose StatementBegin

ALTER TABLE projects ADD COLUMN slack_webhook_url TEXT;
ALTER TABLE projects ADD COLUMN notify_emails TEXT;

CREATE TABLE IF NOT EXISTS notification_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    deployment_id INTEGER REFERENCES deployments(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    environment_id INTEGER REFERENCES environments(id) ON DELETE SET NULL,
    message TEXT NOT NULL,
    results TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_notification_events_created_at ON notification_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notification_events_project_id ON notification_events(project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS notification_events;

-- Note: SQLite <3.35 cannot DROP COLUMN. The added columns on projects
-- remain after Down, same as 003_lifecycles.sql. Manual cleanup required
-- for full rollback.

-- +goose StatementEnd
