-- +goose Up
-- +goose StatementBegin
ALTER TABLE environments ADD COLUMN verification_type TEXT NOT NULL DEFAULT '' CHECK (verification_type IN ('', 'http', 'bash'));
ALTER TABLE environments ADD COLUMN verification_target TEXT NOT NULL DEFAULT '';
ALTER TABLE environments ADD COLUMN verification_timeout_seconds INTEGER NOT NULL DEFAULT 30 CHECK (verification_timeout_seconds BETWEEN 1 AND 300);
ALTER TABLE releases ADD COLUMN snapshot_locked INTEGER NOT NULL DEFAULT 0 CHECK (snapshot_locked IN (0, 1));
UPDATE releases SET snapshot_locked = 1 WHERE EXISTS (SELECT 1 FROM deployments WHERE deployments.release_id = releases.id);
CREATE TABLE deployment_verifications (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('http', 'bash')),
    target TEXT NOT NULL,
    timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds BETWEEN 1 AND 300),
    step_index INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    started_at INTEGER,
    finished_at INTEGER
);
CREATE TABLE deployment_rollbacks (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    source_deployment_id INTEGER NOT NULL,
    target_deployment_id INTEGER NOT NULL,
    source_version TEXT NOT NULL,
    target_version TEXT NOT NULL
);
CREATE INDEX deployment_rollback_lookup ON deployments(environment_id, kind, created_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE verification_rollback_refused (
    guard INTEGER CONSTRAINT verification_requires_forward_migration CHECK (guard = 0)
);
INSERT INTO verification_rollback_refused VALUES (1);
-- +goose StatementEnd
