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

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- +goose StatementEnd
