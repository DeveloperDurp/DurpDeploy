-- +goose Up
ALTER TABLE release_artifacts ADD resolution_key NVARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE deployment_artifacts ADD resolution_key NVARCHAR(64) NOT NULL DEFAULT '';
-- +goose StatementBegin
DECLARE @drop_checks NVARCHAR(MAX) = N'';
SELECT @drop_checks = @drop_checks +
    N'ALTER TABLE [' + OBJECT_NAME(parent_object_id) + N'] DROP CONSTRAINT [' + name + N'];'
FROM sys.check_constraints
WHERE parent_object_id IN (OBJECT_ID('release_artifacts'), OBJECT_ID('deployment_artifacts'))
  AND definition LIKE '%size%';
EXEC sp_executesql @drop_checks;
-- +goose StatementEnd
ALTER TABLE release_artifacts ADD CONSTRAINT ck_release_artifact_pin
CHECK ((sha256 = '' AND size = 0 AND resolution_key <> '') OR (sha256 <> '' AND size > 0));
ALTER TABLE deployment_artifacts ADD CONSTRAINT ck_deployment_artifact_pin
CHECK ((sha256 = '' AND size = 0 AND resolution_key <> '') OR (sha256 <> '' AND size > 0));
CREATE INDEX release_artifact_resolution ON release_artifacts(resolution_key);
CREATE INDEX deployment_artifact_resolution ON deployment_artifacts(resolution_key);

-- +goose Down
-- +goose StatementBegin
CREATE TABLE deferred_artifacts_rollback_refused (
    guard BIGINT CHECK (guard = 0)
);
INSERT INTO deferred_artifacts_rollback_refused VALUES (1);
-- +goose StatementEnd
