-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS approvals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    approved_by TEXT NOT NULL,
    approved_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_approvals_deployment_id ON approvals(deployment_id);

ALTER TABLE lifecycle_stages ADD COLUMN requires_approval INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_approvals_deployment_id;
DROP TABLE IF EXISTS approvals;

-- SQLite <3.35 cannot DROP COLUMN. Leave requires_approval in place on Down;
-- matches the precedent set in 003_lifecycles.sql, 005_step_timeout.sql, 006.

-- +goose StatementEnd
