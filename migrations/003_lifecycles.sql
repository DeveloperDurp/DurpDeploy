-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS lifecycles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS lifecycle_stages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    lifecycle_id INTEGER NOT NULL REFERENCES lifecycles(id) ON DELETE CASCADE,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE RESTRICT,
    sort_order INTEGER NOT NULL,
    UNIQUE(lifecycle_id, sort_order),
    UNIQUE(lifecycle_id, environment_id)
);

CREATE INDEX IF NOT EXISTS idx_lifecycle_stages_lifecycle_id ON lifecycle_stages(lifecycle_id);

ALTER TABLE projects ADD COLUMN lifecycle_id INTEGER REFERENCES lifecycles(id) ON DELETE SET NULL;

ALTER TABLE deployments ADD COLUMN forced INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS lifecycle_stages;
DROP TABLE IF EXISTS lifecycles;

-- Note: SQLite <3.35 cannot DROP COLUMN. The added columns on projects
-- and deployments remain after Down. Manual cleanup required for full rollback.

-- +goose StatementEnd
