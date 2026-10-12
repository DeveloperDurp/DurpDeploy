-- +goose Up
-- Preserve confirmed cleanup when an agent's protocol records are deleted.
ALTER TABLE deployments ADD COLUMN cleanup_confirmed_at INTEGER;

-- +goose Down
CREATE TABLE deployment_cleanup_history_rollback_refused (guard INTEGER CHECK (guard = 0));
INSERT INTO deployment_cleanup_history_rollback_refused VALUES (1);
