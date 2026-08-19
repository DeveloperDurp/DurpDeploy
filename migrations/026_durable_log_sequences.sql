-- +goose Up
-- +goose StatementBegin

DROP INDEX idx_deployment_logs_deployment_sequence;
ALTER TABLE deployment_logs RENAME TO deployment_logs_legacy;
CREATE TABLE deployment_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_name TEXT,
    line TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    sequence INTEGER NOT NULL
);
INSERT INTO deployment_logs (id, deployment_id, step_name, line, created_at, sequence)
    SELECT id, deployment_id, step_name, line, created_at, sequence
    FROM deployment_logs_legacy;
DROP TABLE deployment_logs_legacy;
UPDATE sqlite_sequence
    SET seq = (SELECT MAX(id) FROM deployment_logs)
    WHERE name = 'deployment_logs';
CREATE UNIQUE INDEX idx_deployment_logs_deployment_sequence
    ON deployment_logs(deployment_id, sequence);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX idx_deployment_logs_deployment_sequence;
ALTER TABLE deployment_logs RENAME TO deployment_logs_legacy;
CREATE TABLE deployment_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_name TEXT,
    line TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    sequence INTEGER
);
INSERT INTO deployment_logs (id, deployment_id, step_name, line, created_at, sequence)
    SELECT id, deployment_id, step_name, line, created_at, sequence
    FROM deployment_logs_legacy;
DROP TABLE deployment_logs_legacy;
UPDATE sqlite_sequence
    SET seq = (SELECT MAX(id) FROM deployment_logs)
    WHERE name = 'deployment_logs';
CREATE UNIQUE INDEX idx_deployment_logs_deployment_sequence
    ON deployment_logs(deployment_id, sequence)
    WHERE sequence IS NOT NULL;

-- +goose StatementEnd
