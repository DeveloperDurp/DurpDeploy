-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS step_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    script_body TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_step_templates_name ON step_templates(name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS step_templates;

-- +goose StatementEnd
