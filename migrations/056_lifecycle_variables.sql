-- +goose Up
-- +goose StatementBegin
CREATE TABLE lifecycle_variables (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    lifecycle_id INTEGER NOT NULL REFERENCES lifecycles(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    value TEXT,
    environment_id INTEGER REFERENCES environments(id) ON DELETE CASCADE,
    secret INTEGER NOT NULL DEFAULT 0 CHECK (secret IN (0, 1)),
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE UNIQUE INDEX lifecycle_variables_unscoped
    ON lifecycle_variables(lifecycle_id, name) WHERE environment_id IS NULL;
CREATE UNIQUE INDEX lifecycle_variables_scoped
    ON lifecycle_variables(lifecycle_id, name, environment_id)
    WHERE environment_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
DROP TABLE lifecycle_variables;
