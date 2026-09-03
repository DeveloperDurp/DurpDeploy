-- +goose Up
-- +goose StatementBegin

IF (
    SELECT COUNT(*)
    FROM sys.tables
    WHERE name IN (
        'direct_dispatch_backup_deployment_dispatches',
        'direct_dispatch_backup_agent_pools',
        'direct_dispatch_backup_agent_pool_memberships',
        'direct_dispatch_backup_agent_tags',
        'direct_dispatch_backup_environment_agent_policies'
    )
) <> 5
BEGIN
    THROW 50000, 'direct dispatch rollback backups are missing; restore version 12 before upgrading', 1;
END;
DROP TABLE agent_enrollment_tokens;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE agent_enrollment_tokens (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    token_prefix NVARCHAR(16) NOT NULL CHECK (LEN(token_prefix) <= 16),
    expires_at BIGINT NOT NULL,
    used_at BIGINT NULL,
    revoked_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE INDEX idx_agent_enrollment_tokens_agent_id
    ON agent_enrollment_tokens(agent_id);
CREATE INDEX idx_agent_enrollment_tokens_expires_at
    ON agent_enrollment_tokens(expires_at);

-- +goose StatementEnd
