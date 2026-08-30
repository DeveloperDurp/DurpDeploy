-- +goose Up
-- +goose StatementBegin

DECLARE @ambiguous_environment_ids NVARCHAR(MAX);
SELECT @ambiguous_environment_ids = STRING_AGG(
    CONVERT(NVARCHAR(MAX), environment_id), ','
) WITHIN GROUP (ORDER BY environment_id)
FROM (
    SELECT DISTINCT deployments.environment_id
    FROM deployment_dispatches
    JOIN deployments ON deployments.id = deployment_dispatches.deployment_id
    LEFT JOIN environment_agent_policies
        ON environment_agent_policies.environment_id = deployments.environment_id
    WHERE deployment_dispatches.mode = 'remote'
       AND deployment_dispatches.assigned_agent_id IS NULL
       AND deployment_dispatches.agent_id IS NULL
       AND (
           deployment_dispatches.state <> 'waiting'
           OR (NOT EXISTS (
               SELECT 1 FROM environment_agent_assignments
               JOIN agents ON agents.id = environment_agent_assignments.agent_id
               WHERE environment_agent_assignments.environment_id = deployments.environment_id
                 AND agents.status = 'active'
           ) AND (
               (SELECT COUNT(*) FROM agent_pool_memberships
                WHERE agent_pool_memberships.pool_id = environment_agent_policies.pool_id) <> 1
               OR (SELECT COUNT(*) FROM agent_pool_memberships
                   JOIN agents ON agents.id = agent_pool_memberships.agent_id
                   WHERE agent_pool_memberships.pool_id = environment_agent_policies.pool_id
                     AND agents.status = 'active') <> 1
           ))
       )
) AS ambiguous_environments;
IF @ambiguous_environment_ids IS NOT NULL
BEGIN
    DECLARE @backfill_error NVARCHAR(2047) =
        N'cannot migrate pooled deployment dispatches for environments: '
            + @ambiguous_environment_ids;
    THROW 50000, @backfill_error, 1;
END;

DECLARE @dispatch_assignments TABLE (
    deployment_id BIGINT PRIMARY KEY,
    assigned_agent_id NVARCHAR(255) NOT NULL
);
INSERT INTO @dispatch_assignments (deployment_id, assigned_agent_id)
SELECT
    deployment_dispatches.deployment_id,
    COALESCE(
        deployment_dispatches.assigned_agent_id,
        deployment_dispatches.agent_id,
        CASE WHEN deployment_dispatches.state = 'waiting'
            THEN environment_assignment.agent_id END,
        agent_pool_memberships.agent_id
    )
FROM deployment_dispatches
JOIN deployments ON deployments.id = deployment_dispatches.deployment_id
LEFT JOIN environment_agent_policies
    ON environment_agent_policies.environment_id = deployments.environment_id
OUTER APPLY (
    SELECT TOP 1 environment_agent_assignments.agent_id
    FROM environment_agent_assignments
    JOIN agents ON agents.id = environment_agent_assignments.agent_id
    WHERE environment_agent_assignments.environment_id = deployments.environment_id
      AND agents.status = 'active'
    ORDER BY environment_agent_assignments.updated_at DESC,
        environment_agent_assignments.agent_id
) AS environment_assignment
LEFT JOIN agent_pool_memberships
    ON agent_pool_memberships.pool_id = environment_agent_policies.pool_id
    AND deployment_dispatches.assigned_agent_id IS NULL
    AND deployment_dispatches.agent_id IS NULL
    AND environment_assignment.agent_id IS NULL
    AND EXISTS (
        SELECT 1 FROM agents
        WHERE agents.id = agent_pool_memberships.agent_id
          AND agents.status = 'active'
    )
LEFT JOIN agents ON agents.id = agent_pool_memberships.agent_id
    AND agents.status = 'active'
WHERE deployment_dispatches.mode = 'remote';

SELECT * INTO direct_dispatch_backup_deployment_dispatches
FROM deployment_dispatches;
SELECT * INTO direct_dispatch_backup_agent_pools FROM agent_pools;
SELECT IDENT_CURRENT('agent_pools') AS current_value
INTO direct_dispatch_backup_identities;
SELECT * INTO direct_dispatch_backup_agent_pool_memberships
FROM agent_pool_memberships;
SELECT * INTO direct_dispatch_backup_agent_tags FROM agent_tags;
SELECT * INTO direct_dispatch_backup_environment_agent_policies
FROM environment_agent_policies;

DECLARE @dispatch_check_name NVARCHAR(128), @dispatch_check_sql NVARCHAR(MAX);
DECLARE @dispatch_fk_sql NVARCHAR(MAX) = N'', @dispatch_default_sql NVARCHAR(MAX) = N'';
SELECT @dispatch_check_name = name FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('deployment_dispatches')
  AND definition LIKE '%pool_id%';
SET @dispatch_check_sql = N'ALTER TABLE deployment_dispatches DROP CONSTRAINT ' + QUOTENAME(@dispatch_check_name);
EXEC(@dispatch_check_sql);
SELECT @dispatch_fk_sql = @dispatch_fk_sql + N'ALTER TABLE deployment_dispatches DROP CONSTRAINT ' + QUOTENAME(foreign_key.name) + N';'
FROM sys.foreign_keys AS foreign_key
JOIN sys.foreign_key_columns AS foreign_key_column
  ON foreign_key_column.constraint_object_id = foreign_key.object_id
WHERE foreign_key.parent_object_id = OBJECT_ID('deployment_dispatches')
  AND COL_NAME(foreign_key_column.parent_object_id, foreign_key_column.parent_column_id) = 'pool_id';
EXEC(@dispatch_fk_sql);
SELECT @dispatch_default_sql = @dispatch_default_sql + N'ALTER TABLE deployment_dispatches DROP CONSTRAINT ' + QUOTENAME(default_constraint.name) + N';'
FROM sys.default_constraints AS default_constraint
JOIN sys.columns AS column_definition
  ON column_definition.object_id = default_constraint.parent_object_id
  AND column_definition.column_id = default_constraint.parent_column_id
WHERE default_constraint.parent_object_id = OBJECT_ID('deployment_dispatches')
  AND column_definition.name = 'selector';
EXEC(@dispatch_default_sql);
ALTER TABLE deployment_dispatches DROP COLUMN pool_id;
ALTER TABLE deployment_dispatches DROP COLUMN selector;
UPDATE deployment_dispatches
SET assigned_agent_id = dispatch_assignments.assigned_agent_id
FROM deployment_dispatches
JOIN @dispatch_assignments AS dispatch_assignments
    ON dispatch_assignments.deployment_id = deployment_dispatches.deployment_id
WHERE deployment_dispatches.assigned_agent_id IS NULL;
ALTER TABLE deployment_dispatches ADD CONSTRAINT ck_deployment_dispatches_mode_assignment
CHECK (
    (mode = 'local' AND assigned_agent_id IS NULL)
    OR (mode = 'remote' AND assigned_agent_id IS NOT NULL)
);
DROP TABLE environment_agent_policies;
DROP TABLE agent_tags;
DROP TABLE agent_pool_memberships;
DROP TABLE agent_pools;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE agent_pools (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    name NVARCHAR(255) NOT NULL,
    description NVARCHAR(MAX) NULL,
    enabled BIGINT NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE UNIQUE INDEX idx_agent_pools_name ON agent_pools(name);
CREATE TABLE agent_pool_memberships (
    pool_id BIGINT NOT NULL REFERENCES agent_pools(id) ON DELETE NO ACTION,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (pool_id, agent_id)
);
CREATE INDEX idx_agent_pool_memberships_agent_id ON agent_pool_memberships(agent_id);
CREATE TABLE agent_tags (
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    tag_key NVARCHAR(32) NOT NULL CHECK (LEN(tag_key) BETWEEN 1 AND 32),
    tag_value NVARCHAR(64) NOT NULL CHECK (LEN(tag_value) BETWEEN 1 AND 64),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (agent_id, tag_key)
);
CREATE INDEX idx_agent_tags_agent_id ON agent_tags(agent_id);
CREATE TABLE environment_agent_policies (
    environment_id BIGINT PRIMARY KEY REFERENCES environments(id) ON DELETE NO ACTION,
    pool_id BIGINT NOT NULL REFERENCES agent_pools(id) ON DELETE NO ACTION,
    selector NVARCHAR(MAX) NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE INDEX idx_environment_agent_policies_pool_id ON environment_agent_policies(pool_id);

INSERT INTO agent_pools (name)
SELECT agent_id
FROM (
    SELECT agent_id FROM environment_agent_assignments
    UNION
    SELECT assigned_agent_id
    FROM deployment_dispatches
    WHERE assigned_agent_id IS NOT NULL
) AS agent_sources
WHERE NOT EXISTS (
    SELECT 1 FROM agent_pools WHERE agent_pools.name = agent_sources.agent_id
);
INSERT INTO agent_pool_memberships (pool_id, agent_id)
SELECT agent_pools.id, agent_sources.agent_id
FROM (
    SELECT agent_id FROM environment_agent_assignments
    UNION
    SELECT assigned_agent_id
    FROM deployment_dispatches
    WHERE assigned_agent_id IS NOT NULL
) AS agent_sources
JOIN agent_pools ON agent_pools.name = agent_sources.agent_id
WHERE NOT EXISTS (
    SELECT 1
    FROM agent_pool_memberships
    WHERE agent_pool_memberships.pool_id = agent_pools.id
      AND agent_pool_memberships.agent_id = agent_sources.agent_id
);
INSERT INTO environment_agent_policies (environment_id, pool_id)
SELECT environment_agent_assignments.environment_id, agent_pools.id
FROM environment_agent_assignments
JOIN agent_pools ON agent_pools.name = environment_agent_assignments.agent_id
WHERE NOT EXISTS (
    SELECT 1
    FROM environment_agent_policies
    WHERE environment_agent_policies.environment_id = environment_agent_assignments.environment_id
);
ALTER TABLE deployment_dispatches DROP CONSTRAINT ck_deployment_dispatches_mode_assignment;
EXEC(N'ALTER TABLE deployment_dispatches ADD pool_id BIGINT NULL REFERENCES agent_pools(id) ON DELETE NO ACTION;');
EXEC(N'ALTER TABLE deployment_dispatches ADD selector NVARCHAR(MAX) NOT NULL DEFAULT '''';');
UPDATE deployment_dispatches
SET pool_id = agent_pool_memberships.pool_id,
    assigned_agent_id = NULL
FROM deployment_dispatches
JOIN agent_pool_memberships
    ON agent_pool_memberships.agent_id = deployment_dispatches.assigned_agent_id
WHERE deployment_dispatches.mode = 'remote';
DELETE FROM deployment_dispatches;
DELETE FROM environment_agent_policies;
DELETE FROM agent_tags;
DELETE FROM agent_pool_memberships;
DELETE FROM agent_pools;
SET IDENTITY_INSERT agent_pools ON;
INSERT INTO agent_pools (
    id, name, description, enabled, created_at, updated_at
)
SELECT id, name, description, enabled, created_at, updated_at
FROM direct_dispatch_backup_agent_pools;
SET IDENTITY_INSERT agent_pools OFF;
DECLARE @direct_dispatch_identity BIGINT;
DECLARE @direct_dispatch_identity_sql NVARCHAR(128);
SELECT @direct_dispatch_identity = current_value
FROM direct_dispatch_backup_identities;
SET @direct_dispatch_identity_sql = N'DBCC CHECKIDENT (''agent_pools'', RESEED, '
    + CONVERT(NVARCHAR(20), @direct_dispatch_identity) + N')';
EXEC(@direct_dispatch_identity_sql);
INSERT INTO agent_pool_memberships (pool_id, agent_id, created_at)
SELECT pool_id, agent_id, created_at
FROM direct_dispatch_backup_agent_pool_memberships;
INSERT INTO agent_tags (agent_id, tag_key, tag_value, created_at)
SELECT agent_id, tag_key, tag_value, created_at
FROM direct_dispatch_backup_agent_tags;
INSERT INTO environment_agent_policies (
    environment_id, pool_id, selector, created_at, updated_at
)
SELECT environment_id, pool_id, selector, created_at, updated_at
FROM direct_dispatch_backup_environment_agent_policies;
EXEC(N'
    INSERT INTO deployment_dispatches (
        deployment_id, mode, pool_id, selector, state, reason, agent_id,
        assigned_agent_id, claim_token_hash, claim_expires_at, started_at,
        finished_at, last_heartbeat_at, cancel_requested_at, created_at,
        updated_at
    )
    SELECT
        deployment_id, mode, pool_id, selector, state, reason, agent_id,
        assigned_agent_id, claim_token_hash, claim_expires_at, started_at,
        finished_at, last_heartbeat_at, cancel_requested_at, created_at,
        updated_at
    FROM direct_dispatch_backup_deployment_dispatches;');
DROP TABLE direct_dispatch_backup_deployment_dispatches;
DROP TABLE direct_dispatch_backup_agent_pools;
DROP TABLE direct_dispatch_backup_identities;
DROP TABLE direct_dispatch_backup_agent_pool_memberships;
DROP TABLE direct_dispatch_backup_agent_tags;
DROP TABLE direct_dispatch_backup_environment_agent_policies;
EXEC(N'ALTER TABLE deployment_dispatches ADD CONSTRAINT ck_deployment_dispatches_mode_assignment CHECK ((mode = ''local'' AND pool_id IS NULL AND assigned_agent_id IS NULL) OR (mode = ''remote'' AND ((pool_id IS NOT NULL AND assigned_agent_id IS NULL) OR (pool_id IS NULL AND assigned_agent_id IS NOT NULL))));');
-- +goose StatementEnd
