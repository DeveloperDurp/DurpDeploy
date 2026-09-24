-- +goose Up
ALTER TABLE releases ADD kind NVARCHAR(32) NOT NULL
    CONSTRAINT DF_releases_kind DEFAULT 'deployment';
ALTER TABLE deployments ADD kind NVARCHAR(32) NOT NULL
    CONSTRAINT DF_deployments_kind DEFAULT 'deployment';

CREATE TABLE runbooks (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name NVARCHAR(255) NOT NULL,
    description NVARCHAR(MAX) NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND,'1970-01-01',SYSUTCDATETIME()),
    CONSTRAINT uq_runbooks_project_name UNIQUE(project_id, name)
);
CREATE TABLE runbook_versions (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    runbook_id BIGINT NOT NULL REFERENCES runbooks(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    release_id BIGINT NOT NULL UNIQUE REFERENCES releases(id),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND,'1970-01-01',SYSUTCDATETIME()),
    CONSTRAINT uq_runbook_versions_number UNIQUE(runbook_id, version)
);
CREATE TABLE runbook_schedules (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    runbook_id BIGINT NOT NULL REFERENCES runbooks(id) ON DELETE CASCADE,
    version_id BIGINT NULL REFERENCES runbook_versions(id),
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    cron NVARCHAR(255) NOT NULL,
    next_run_at BIGINT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 1,
    last_fired_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND,'1970-01-01',SYSUTCDATETIME())
);
CREATE INDEX idx_runbook_schedules_due ON runbook_schedules(next_run_at)
    WHERE enabled = 1;
CREATE TABLE runbook_executions (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    runbook_version_id BIGINT NOT NULL REFERENCES runbook_versions(id),
    deployment_id BIGINT NOT NULL UNIQUE REFERENCES deployments(id),
    actor_user_id BIGINT NULL REFERENCES users(id),
    schedule_id BIGINT NULL REFERENCES runbook_schedules(id),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND,'1970-01-01',SYSUTCDATETIME())
);
CREATE INDEX idx_runbook_executions_version ON runbook_executions(runbook_version_id);

-- +goose Down
CREATE TABLE runbooks_rollback_refused (
    guard BIGINT CONSTRAINT runbooks_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO runbooks_rollback_refused VALUES (1);
