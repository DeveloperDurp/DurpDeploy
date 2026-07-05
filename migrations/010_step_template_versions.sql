-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS step_template_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    template_id INTEGER NOT NULL REFERENCES step_templates(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL,
    name TEXT NOT NULL,
    script_body TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(template_id, version_number)
);
CREATE INDEX IF NOT EXISTS idx_step_template_versions_template_id ON step_template_versions(template_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS step_template_versions;
-- +goose StatementEnd
