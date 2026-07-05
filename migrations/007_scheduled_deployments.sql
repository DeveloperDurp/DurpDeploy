-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS scheduled_deployments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    release_id INTEGER NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    cron TEXT NOT NULL,
    next_run_at INTEGER NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_fired_at INTEGER,
    note TEXT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_scheduled_deployments_due ON scheduled_deployments(next_run_at) WHERE enabled = 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS scheduled_deployments;

-- +goose StatementEnd
