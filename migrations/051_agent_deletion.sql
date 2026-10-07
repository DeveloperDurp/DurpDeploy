-- +goose Up
ALTER TABLE agents ADD COLUMN deleted_at INTEGER
    CHECK (deleted_at IS NULL OR status = 'revoked');

-- +goose Down
-- Removing tombstones would restore deleted identities to the inventory.
CREATE TABLE agent_deletion_rollback_refused (
    guard INTEGER CONSTRAINT agent_deletion_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO agent_deletion_rollback_refused VALUES (1);
