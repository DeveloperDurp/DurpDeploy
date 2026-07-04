-- +goose Up
-- +goose StatementBegin

ALTER TABLE variables ADD COLUMN secret INTEGER NOT NULL DEFAULT 0;
ALTER TABLE release_variables ADD COLUMN secret INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN on older versions; recreate tables without the column.
CREATE TABLE variables_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    value TEXT,
    environment_id INTEGER REFERENCES environments(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(name, project_id, environment_id)
);
INSERT INTO variables_new SELECT id, project_id, name, value, environment_id, created_at FROM variables;
DROP TABLE variables;
ALTER TABLE variables_new RENAME TO variables;

CREATE TABLE release_variables_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    release_id INTEGER NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    value TEXT,
    environment_id INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
INSERT INTO release_variables_new SELECT id, release_id, name, value, environment_id, created_at FROM release_variables;
DROP TABLE release_variables;
ALTER TABLE release_variables_new RENAME TO release_variables;

-- +goose StatementEnd
