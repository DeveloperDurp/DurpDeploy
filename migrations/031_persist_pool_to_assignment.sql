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

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- +goose StatementEnd
