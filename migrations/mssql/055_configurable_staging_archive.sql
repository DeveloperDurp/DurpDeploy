-- +goose Up
-- The historical size constraint has a database-generated name.
-- +goose StatementBegin
DECLARE @constraint sysname;
SELECT @constraint = name FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID(N'dbo.deployment_staging_snapshots')
  AND parent_column_id = COLUMNPROPERTY(
      OBJECT_ID(N'dbo.deployment_staging_snapshots'), N'size', 'ColumnId');
IF @constraint IS NULL
    THROW 50000, 'The staging size constraint is missing.', 1;
DECLARE @statement nvarchar(max);
SET @statement = N'ALTER TABLE dbo.deployment_staging_snapshots DROP CONSTRAINT ' + QUOTENAME(@constraint);
EXEC sp_executesql @statement;
ALTER TABLE dbo.deployment_staging_snapshots WITH CHECK
    ADD CONSTRAINT ck_deployment_staging_size CHECK (size > 0);
-- +goose StatementEnd

-- +goose Down
-- Validation refuses rollback when any saved size exceeds the old ceiling.
ALTER TABLE dbo.deployment_staging_snapshots DROP CONSTRAINT ck_deployment_staging_size;
ALTER TABLE dbo.deployment_staging_snapshots WITH CHECK
    ADD CONSTRAINT ck_deployment_staging_size CHECK (size > 0 AND size <= 570425344);
