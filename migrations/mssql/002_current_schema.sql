-- SQL Server additions corresponding to migrations 002-019.
-- This migration is intentionally consolidated for new SQL Server databases.
-- +goose Up

CREATE TABLE step_templates (
    id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    name NVARCHAR(255) NOT NULL UNIQUE,
    script_body NVARCHAR(MAX) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);

CREATE TABLE lifecycles (
    id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    name NVARCHAR(255) NOT NULL UNIQUE,
    description NVARCHAR(MAX),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
ALTER TABLE projects ADD CONSTRAINT fk_projects_lifecycle
    FOREIGN KEY (lifecycle_id) REFERENCES lifecycles(id) ON DELETE SET NULL;
CREATE TABLE lifecycle_stages (
    id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    lifecycle_id BIGINT NOT NULL REFERENCES lifecycles(id) ON DELETE CASCADE,
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    sort_order BIGINT NOT NULL,
    requires_approval BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT uq_lifecycle_stage_order UNIQUE(lifecycle_id, sort_order),
    CONSTRAINT uq_lifecycle_stage_environment UNIQUE(lifecycle_id, environment_id)
);
CREATE TABLE scheduled_deployments (
    id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    release_id BIGINT NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    environment_id BIGINT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    cron NVARCHAR(255) NOT NULL,
    next_run_at BIGINT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 1,
    last_fired_at BIGINT NULL,
    note NVARCHAR(MAX) NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);

CREATE TABLE step_template_versions (
    id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    template_id BIGINT NOT NULL REFERENCES step_templates(id) ON DELETE CASCADE,
    version_number BIGINT NOT NULL,
    name NVARCHAR(255) NOT NULL,
    script_body NVARCHAR(MAX) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    UNIQUE(template_id, version_number)
);

CREATE TABLE project_members (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role NVARCHAR(32) NOT NULL, created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY(project_id, user_id)
);
CREATE TABLE deployment_approvals (
    id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY, deployment_id BIGINT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    approved_by NVARCHAR(255) NOT NULL, approved_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    approver_user_id BIGINT NULL REFERENCES users(id), required_approver_role NVARCHAR(32) NOT NULL
);
CREATE TABLE notification_events (
    id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY, event_type NVARCHAR(255) NOT NULL,
    deployment_id BIGINT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    project_id BIGINT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id BIGINT NULL,
    message NVARCHAR(MAX) NOT NULL, results NVARCHAR(MAX) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE TABLE global_notifications (
    id BIGINT NOT NULL PRIMARY KEY, slack_webhook_url NVARCHAR(MAX), notify_emails NVARCHAR(MAX),
    gotify_url NVARCHAR(MAX), gotify_token NVARCHAR(MAX), discord_webhook_url NVARCHAR(MAX),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
INSERT INTO global_notifications (id) VALUES (1);
CREATE TABLE api_tokens (
    id NVARCHAR(255) NOT NULL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name NVARCHAR(255) NOT NULL, token_prefix NVARCHAR(255) NOT NULL, token_hash NVARCHAR(MAX) NOT NULL,
    scope NVARCHAR(255) NOT NULL, last_used_at BIGINT NULL, expires_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()), revoked_at BIGINT NULL
);

-- +goose StatementBegin
CREATE TRIGGER trg_projects_delete ON projects
INSTEAD OF DELETE AS
BEGIN
    SET NOCOUNT ON;
    DELETE FROM releases WHERE project_id IN (SELECT id FROM deleted);
    DELETE FROM projects WHERE id IN (SELECT id FROM deleted);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_environments_delete ON environments
INSTEAD OF DELETE AS
BEGIN
    SET NOCOUNT ON;
    DELETE FROM deployments WHERE environment_id IN (SELECT id FROM deleted);
    DELETE FROM environments WHERE id IN (SELECT id FROM deleted);
END;
-- +goose StatementEnd
