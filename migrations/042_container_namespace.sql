-- +goose Up
ALTER TABLE deployments ADD COLUMN container_namespace TEXT;

-- +goose Down
-- +goose StatementBegin
CREATE TABLE container_namespace_rollback_refused (
    guard INTEGER CONSTRAINT container_namespace_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO container_namespace_rollback_refused VALUES (1);
-- +goose StatementEnd
