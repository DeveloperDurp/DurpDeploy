-- +goose Up

CREATE TABLE agent_pairings (
    agent_id NVARCHAR(255) NOT NULL PRIMARY KEY REFERENCES agents(id) ON DELETE NO ACTION,
    pairing_code_hash BINARY(32) NOT NULL UNIQUE,
    agent_public_identity NVARCHAR(MAX) NOT NULL,
    agent_pin NVARCHAR(64) NOT NULL UNIQUE CHECK (LEN(agent_pin) = 64),
    server_public_identity NVARCHAR(MAX) NULL,
    server_pin NVARCHAR(64) NULL CHECK (server_pin IS NULL OR LEN(server_pin) = 64),
    state NVARCHAR(16) NOT NULL CHECK (state IN ('pending', 'paired', 'expired')),
    expires_at BIGINT NOT NULL,
    paired_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CHECK (
        (state = 'pending' AND server_public_identity IS NULL
            AND server_pin IS NULL AND paired_at IS NULL)
        OR (state = 'paired' AND server_public_identity IS NOT NULL
            AND server_pin IS NOT NULL AND paired_at IS NOT NULL)
        OR (state = 'expired' AND server_public_identity IS NULL
            AND server_pin IS NULL AND paired_at IS NULL)
    )
);
CREATE INDEX idx_agent_pairings_state_expires_at
    ON agent_pairings(state, expires_at);

CREATE TABLE environment_agent_assignments (
    environment_id BIGINT NOT NULL PRIMARY KEY
        REFERENCES environments(id) ON DELETE NO ACTION,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE INDEX idx_environment_agent_assignments_agent_id
    ON environment_agent_assignments(agent_id);

ALTER TABLE deployment_dispatches
    ADD assigned_agent_id NVARCHAR(255) NULL REFERENCES agents(id) ON DELETE NO ACTION;
DECLARE @dispatch_check_name NVARCHAR(128), @dispatch_check_sql NVARCHAR(MAX)
SELECT @dispatch_check_name = name
FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('deployment_dispatches')
  AND definition LIKE '%pool_id%'
SET @dispatch_check_sql = N'ALTER TABLE deployment_dispatches DROP CONSTRAINT ' + QUOTENAME(@dispatch_check_name)
EXEC(@dispatch_check_sql);
ALTER TABLE deployment_dispatches ADD CONSTRAINT ck_deployment_dispatches_mode_assignment
CHECK (
    (mode = 'local' AND pool_id IS NULL AND assigned_agent_id IS NULL)
    OR (mode = 'remote' AND (
        (pool_id IS NOT NULL AND assigned_agent_id IS NULL)
        OR (pool_id IS NULL AND assigned_agent_id IS NOT NULL)
    ))
);
CREATE INDEX idx_deployment_dispatches_assigned_agent_state
    ON deployment_dispatches(assigned_agent_id, state);
CREATE UNIQUE INDEX idx_deployment_dispatches_active_agent
    ON deployment_dispatches(agent_id)
    WHERE agent_id IS NOT NULL
      AND state IN ('claimed', 'started', 'cancel_requested');

-- +goose Down

DROP INDEX idx_deployment_dispatches_active_agent ON deployment_dispatches;
DROP INDEX idx_deployment_dispatches_assigned_agent_state ON deployment_dispatches;
ALTER TABLE deployment_dispatches DROP CONSTRAINT ck_deployment_dispatches_mode_assignment;
ALTER TABLE deployment_dispatches ADD CONSTRAINT ck_deployment_dispatches_mode
CHECK (
    (mode = 'local' AND pool_id IS NULL)
    OR (mode = 'remote' AND pool_id IS NOT NULL)
);
DECLARE @assigned_agent_fk_name NVARCHAR(128), @assigned_agent_fk_sql NVARCHAR(MAX)
SELECT @assigned_agent_fk_name = name
FROM sys.foreign_keys AS foreign_key
JOIN sys.foreign_key_columns AS foreign_key_column
  ON foreign_key_column.constraint_object_id = foreign_key.object_id
WHERE foreign_key.parent_object_id = OBJECT_ID('deployment_dispatches')
  AND foreign_key.referenced_object_id = OBJECT_ID('agents')
  AND COL_NAME(
      foreign_key_column.parent_object_id,
      foreign_key_column.parent_column_id
  ) = 'assigned_agent_id'
SET @assigned_agent_fk_sql = N'ALTER TABLE deployment_dispatches DROP CONSTRAINT ' + QUOTENAME(@assigned_agent_fk_name)
EXEC(@assigned_agent_fk_sql);
ALTER TABLE deployment_dispatches DROP COLUMN assigned_agent_id;
DROP TABLE environment_agent_assignments;
DROP TABLE agent_pairings;
