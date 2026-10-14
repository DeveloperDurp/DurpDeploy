-- +goose Up
ALTER TABLE runbooks ADD COLUMN deleted_at INTEGER;

-- +goose Down
-- Refuse rollback because it would make deleted runbooks executable again.
CREATE TABLE runbook_deletion_rollback_refused (
    guard INTEGER CHECK (guard = 0)
);
INSERT INTO runbook_deletion_rollback_refused VALUES (1);
