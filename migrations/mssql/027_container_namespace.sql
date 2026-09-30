-- +goose Up
ALTER TABLE deployments ADD container_namespace NVARCHAR(255) NULL;

-- +goose Down
-- +goose StatementBegin
CREATE TABLE container_namespace_rollback_refused (
    guard BIGINT CONSTRAINT container_namespace_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO container_namespace_rollback_refused VALUES (1);
-- +goose StatementEnd
