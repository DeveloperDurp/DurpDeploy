-- +goose Up
-- +goose StatementBegin

CREATE TABLE agent_labels (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    name NVARCHAR(255) NOT NULL,
    normalized_name NVARCHAR(255) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CONSTRAINT ck_agent_labels_name CHECK (
        LEN(LTRIM(RTRIM(name))) BETWEEN 1 AND 255
        AND normalized_name = LOWER(LTRIM(RTRIM(name)))
    ),
    CONSTRAINT uq_agent_labels_normalized_name UNIQUE (normalized_name)
);
CREATE TABLE agent_label_memberships (
    agent_label_id BIGINT NOT NULL REFERENCES agent_labels(id) ON DELETE CASCADE,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (agent_label_id, agent_id)
);
CREATE INDEX idx_agent_label_memberships_agent ON agent_label_memberships(agent_id);

CREATE TABLE project_execution_policies (
    project_id BIGINT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    target_mode NVARCHAR(16) NOT NULL CHECK (target_mode IN ('local', 'label')),
    agent_label_id BIGINT NULL REFERENCES agent_labels(id) ON DELETE NO ACTION,
    agent_strategy NVARCHAR(32) NULL CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CONSTRAINT ck_project_execution_policy CHECK (
        (target_mode = 'local' AND agent_label_id IS NULL AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL AND agent_strategy IS NOT NULL)
    )
);
CREATE INDEX idx_project_execution_policies_label ON project_execution_policies(agent_label_id);

CREATE TABLE scheduled_deployment_routing_policies (
    scheduled_deployment_id BIGINT PRIMARY KEY
        REFERENCES scheduled_deployments(id) ON DELETE CASCADE,
    target_mode NVARCHAR(16) NOT NULL CHECK (target_mode IN ('inherit', 'local', 'label')),
    agent_label_id BIGINT NULL REFERENCES agent_labels(id) ON DELETE NO ACTION,
    agent_strategy NVARCHAR(32) NULL CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CONSTRAINT ck_scheduled_deployment_routing_policy CHECK (
        (target_mode IN ('inherit', 'local') AND agent_label_id IS NULL AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL AND agent_strategy IS NOT NULL)
    )
);
CREATE INDEX idx_scheduled_routing_policies_label ON scheduled_deployment_routing_policies(agent_label_id);

ALTER TABLE deployments ADD parent_deployment_id BIGINT NULL
    REFERENCES deployments(id) ON DELETE NO ACTION;
ALTER TABLE deployments ADD target_agent_id NVARCHAR(255) NULL;
ALTER TABLE deployments ADD target_agent_name NVARCHAR(255) NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE deployments ADD CONSTRAINT ck_deployments_not_self_parent
    CHECK (parent_deployment_id IS NULL OR parent_deployment_id <> id);
ALTER TABLE deployments ADD CONSTRAINT ck_deployments_target_agent_identity
    CHECK ((target_agent_id IS NULL AND target_agent_name IS NULL)
        OR (target_agent_id IS NOT NULL AND target_agent_name IS NOT NULL));
CREATE INDEX idx_deployments_parent ON deployments(parent_deployment_id);

EXEC(N'CREATE TRIGGER deployments_root_parent_guard ON deployments
AFTER INSERT, UPDATE AS
BEGIN
    SET NOCOUNT ON;
    IF EXISTS (
        SELECT 1 FROM inserted AS child
        JOIN deployments AS parent ON parent.id = child.parent_deployment_id
        WHERE parent.parent_deployment_id IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM inserted AS parent
        JOIN deployments AS child ON child.parent_deployment_id = parent.id
        WHERE parent.parent_deployment_id IS NOT NULL
    )
    BEGIN
        THROW 50000, ''deployment parent must be a root'', 1;
    END;
END');

CREATE TABLE deployment_routing_snapshots (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    source NVARCHAR(16) NOT NULL CHECK (source IN ('legacy', 'project', 'request', 'schedule', 'retry', 'redeploy')),
    target_mode NVARCHAR(16) NOT NULL CHECK (target_mode IN ('local', 'label')),
    agent_label_id BIGINT NULL REFERENCES agent_labels(id) ON DELETE NO ACTION,
    agent_label_name NVARCHAR(255) NULL,
    agent_strategy NVARCHAR(32) NULL CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CONSTRAINT ck_deployment_routing_snapshot CHECK (
        (target_mode = 'local' AND agent_label_id IS NULL AND agent_label_name IS NULL AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL AND agent_label_name IS NOT NULL AND agent_strategy IS NOT NULL)
    )
);
CREATE INDEX idx_deployment_routing_snapshots_label ON deployment_routing_snapshots(agent_label_id);
CREATE TABLE deployment_routing_agents (
    deployment_id BIGINT NOT NULL REFERENCES deployment_routing_snapshots(deployment_id) ON DELETE CASCADE,
    position BIGINT NOT NULL CHECK (position >= 0),
    agent_id NVARCHAR(255) NOT NULL,
    agent_name NVARCHAR(255) NOT NULL,
    PRIMARY KEY (deployment_id, position),
    UNIQUE (deployment_id, agent_id)
);
CREATE TABLE agent_label_cursors (
    agent_label_id BIGINT PRIMARY KEY REFERENCES agent_labels(id) ON DELETE CASCADE,
    last_agent_id NVARCHAR(255) NULL,
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE TABLE scheduled_deployment_occurrences (
    scheduled_deployment_id BIGINT NOT NULL
        REFERENCES scheduled_deployments(id) ON DELETE NO ACTION,
    due_at BIGINT NOT NULL,
    deployment_id BIGINT NOT NULL UNIQUE
        REFERENCES deployments(id) ON DELETE NO ACTION,
    routing_source NVARCHAR(32) NOT NULL CHECK (
        routing_source IN ('legacy', 'project', 'schedule')
    ),
    target_mode NVARCHAR(32) NOT NULL CHECK (
        target_mode IN ('local', 'label', 'remote')
    ),
    agent_label_id BIGINT NULL REFERENCES agent_labels(id) ON DELETE NO ACTION,
    agent_label_name NVARCHAR(255) NULL,
    agent_strategy NVARCHAR(32) NULL CHECK (
        agent_strategy IN ('round_robin', 'all')
    ),
    legacy_agent_id NVARCHAR(255) NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (scheduled_deployment_id, due_at),
    CHECK (
        (target_mode = 'local' AND agent_label_id IS NULL
            AND agent_label_name IS NULL AND agent_strategy IS NULL
            AND legacy_agent_id IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL
            AND agent_label_name IS NOT NULL AND agent_strategy IS NOT NULL
            AND legacy_agent_id IS NULL)
        OR (target_mode = 'remote' AND agent_label_id IS NULL
            AND agent_label_name IS NULL AND agent_strategy IS NULL
            AND legacy_agent_id IS NOT NULL)
    )
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

IF EXISTS (SELECT 1 FROM deployments WHERE parent_deployment_id IS NOT NULL)
    THROW 50000, 'cannot downgrade agent label routing with fan-out history', 1;
DROP TABLE scheduled_deployment_occurrences;
DROP TABLE agent_label_cursors;
DROP TABLE deployment_routing_agents;
DROP TABLE deployment_routing_snapshots;
DROP TRIGGER deployments_root_parent_guard;
DROP INDEX idx_deployments_parent ON deployments;
ALTER TABLE deployments DROP CONSTRAINT ck_deployments_not_self_parent;
ALTER TABLE deployments DROP CONSTRAINT ck_deployments_target_agent_identity;
DECLARE @routing_fk_sql NVARCHAR(MAX) = N'';
SELECT @routing_fk_sql = @routing_fk_sql
    + N'ALTER TABLE deployments DROP CONSTRAINT ' + QUOTENAME(foreign_key.name) + N';'
FROM sys.foreign_keys AS foreign_key
JOIN sys.foreign_key_columns AS foreign_key_column
  ON foreign_key_column.constraint_object_id = foreign_key.object_id
WHERE foreign_key.parent_object_id = OBJECT_ID('deployments')
  AND COL_NAME(foreign_key_column.parent_object_id, foreign_key_column.parent_column_id)
      = 'parent_deployment_id';
EXEC(@routing_fk_sql);
ALTER TABLE deployments DROP COLUMN target_agent_name, target_agent_id, parent_deployment_id;
DROP TABLE scheduled_deployment_routing_policies;
DROP TABLE project_execution_policies;
DROP TABLE agent_label_memberships;
DROP TABLE agent_labels;

-- +goose StatementEnd
