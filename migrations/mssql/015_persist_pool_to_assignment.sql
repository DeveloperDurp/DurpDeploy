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

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- +goose StatementEnd
