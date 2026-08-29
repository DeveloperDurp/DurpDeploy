-- +goose Up
-- +goose StatementBegin

DO $$
BEGIN
    IF (
        SELECT COUNT(*)
        FROM pg_tables
        WHERE schemaname = current_schema()
          AND tablename IN (
              'direct_dispatch_backup_deployment_dispatches',
              'direct_dispatch_backup_agent_pools',
              'direct_dispatch_backup_agent_pool_memberships',
              'direct_dispatch_backup_agent_tags',
              'direct_dispatch_backup_environment_agent_policies'
          )
    ) <> 5 THEN
        RAISE EXCEPTION 'direct dispatch rollback backups are missing; restore version 27 before upgrading';
    END IF;
END $$;
DROP TABLE agent_enrollment_tokens;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE agent_enrollment_tokens (
    token_hash BYTEA PRIMARY KEY CHECK (length(token_hash) = 32),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    token_prefix TEXT NOT NULL CHECK (length(token_prefix) <= 16),
    expires_at BIGINT NOT NULL,
    used_at BIGINT,
    revoked_at BIGINT,
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())
);
CREATE INDEX idx_agent_enrollment_tokens_agent_id
    ON agent_enrollment_tokens(agent_id);
CREATE INDEX idx_agent_enrollment_tokens_expires_at
    ON agent_enrollment_tokens(expires_at);

-- +goose StatementEnd
