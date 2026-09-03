-- +goose Up
-- +goose StatementBegin

DECLARE @state_check NVARCHAR(128), @state_sql NVARCHAR(MAX);
SELECT @state_check = name FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('agent_pairings')
  AND definition LIKE '%''pending''%'
  AND definition LIKE '%''paired''%'
  AND definition LIKE '%''expired''%'
  AND definition NOT LIKE '%server_public_identity%';
SET @state_sql = N'ALTER TABLE agent_pairings DROP CONSTRAINT ' + QUOTENAME(@state_check);
EXEC(@state_sql);
ALTER TABLE agent_pairings ADD CONSTRAINT ck_agent_pairings_state
    CHECK (state IN ('pending', 'committing', 'paired', 'expired'));
DECLARE @pairing_check NVARCHAR(128), @pairing_sql NVARCHAR(MAX);
SELECT @pairing_check = name FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('agent_pairings')
  AND definition LIKE '%server_public_identity%paired_at%';
SET @pairing_sql = N'ALTER TABLE agent_pairings DROP CONSTRAINT ' + QUOTENAME(@pairing_check);
EXEC(@pairing_sql);
ALTER TABLE agent_pairings ADD CONSTRAINT ck_agent_pairings_values CHECK (
    (state IN ('pending', 'committing', 'expired')
        AND server_public_identity IS NULL AND server_pin IS NULL AND paired_at IS NULL)
    OR (state = 'paired' AND server_public_identity IS NOT NULL
        AND server_pin IS NOT NULL AND paired_at IS NOT NULL)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM agent_pairings WHERE state = 'committing';
ALTER TABLE agent_pairings DROP CONSTRAINT ck_agent_pairings_state;
ALTER TABLE agent_pairings ADD CONSTRAINT ck_agent_pairings_state
    CHECK (state IN ('pending', 'paired', 'expired'));
ALTER TABLE agent_pairings DROP CONSTRAINT ck_agent_pairings_values;
ALTER TABLE agent_pairings ADD CONSTRAINT ck_agent_pairings_values CHECK (
    (state = 'pending' AND server_public_identity IS NULL
        AND server_pin IS NULL AND paired_at IS NULL)
    OR (state = 'paired' AND server_public_identity IS NOT NULL
        AND server_pin IS NOT NULL AND paired_at IS NOT NULL)
    OR (state = 'expired' AND server_public_identity IS NULL
        AND server_pin IS NULL AND paired_at IS NULL)
);

-- +goose StatementEnd
