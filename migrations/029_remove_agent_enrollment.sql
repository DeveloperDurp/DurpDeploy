-- +goose Up
-- +goose StatementBegin

CREATE TEMP TABLE direct_dispatch_backup_guard (missing INTEGER);
CREATE TEMP TRIGGER direct_dispatch_backup_abort
BEFORE INSERT ON direct_dispatch_backup_guard
WHEN NEW.missing = 1
BEGIN
    SELECT RAISE(ABORT, 'direct dispatch rollback backups are missing; restore version 27 before upgrading');
END;
INSERT INTO direct_dispatch_backup_guard (missing)
SELECT COUNT(*) <> 5
FROM sqlite_master
WHERE type = 'table'
  AND name IN (
      'direct_dispatch_backup_deployment_dispatches',
      'direct_dispatch_backup_agent_pools',
      'direct_dispatch_backup_agent_pool_memberships',
      'direct_dispatch_backup_agent_tags',
      'direct_dispatch_backup_environment_agent_policies'
  );
DROP TRIGGER direct_dispatch_backup_abort;
DROP TABLE direct_dispatch_backup_guard;
DROP TABLE agent_enrollment_tokens;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE agent_enrollment_tokens (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    token_prefix TEXT NOT NULL CHECK (length(token_prefix) <= 16),
    expires_at INTEGER NOT NULL,
    used_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_agent_enrollment_tokens_agent_id
    ON agent_enrollment_tokens(agent_id);
CREATE INDEX idx_agent_enrollment_tokens_expires_at
    ON agent_enrollment_tokens(expires_at);

-- +goose StatementEnd
