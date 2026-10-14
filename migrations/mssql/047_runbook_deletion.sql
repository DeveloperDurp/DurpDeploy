-- +goose Up
ALTER TABLE runbooks ADD deleted_at BIGINT NULL;

-- +goose Down
-- Refuse rollback because it would make deleted runbooks executable again.
THROW 51000, 'Runbook deletion requires a forward migration', 1;
