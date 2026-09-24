-- +goose Up
-- +goose StatementBegin
ALTER TABLE releases ADD COLUMN kind TEXT NOT NULL DEFAULT 'deployment'
    CHECK (kind IN ('deployment', 'runbook'));
ALTER TABLE deployments ADD COLUMN kind TEXT NOT NULL DEFAULT 'deployment'
    CHECK (kind IN ('deployment', 'runbook'));

CREATE TABLE runbooks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(project_id, name)
);

CREATE TABLE runbook_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    runbook_id INTEGER NOT NULL REFERENCES runbooks(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    release_id INTEGER NOT NULL UNIQUE REFERENCES releases(id),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(runbook_id, version)
);

CREATE TABLE runbook_schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    runbook_id INTEGER NOT NULL REFERENCES runbooks(id) ON DELETE CASCADE,
    version_id INTEGER REFERENCES runbook_versions(id),
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    cron TEXT NOT NULL,
    next_run_at INTEGER NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_fired_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_runbook_schedules_due ON runbook_schedules(next_run_at)
    WHERE enabled = 1;

CREATE TABLE runbook_executions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    runbook_version_id INTEGER NOT NULL REFERENCES runbook_versions(id),
    deployment_id INTEGER NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    schedule_id INTEGER REFERENCES runbook_schedules(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_runbook_executions_version ON runbook_executions(runbook_version_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE runbooks_rollback_refused (
    guard INTEGER CONSTRAINT runbooks_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO runbooks_rollback_refused VALUES (1);
-- +goose StatementEnd
