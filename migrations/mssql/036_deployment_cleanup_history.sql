-- +goose Up
ALTER TABLE deployments ADD cleanup_confirmed_at BIGINT NULL;

-- +goose Down
CREATE TABLE deployment_cleanup_history_rollback_refused (guard BIGINT CHECK (guard = 0));
INSERT INTO deployment_cleanup_history_rollback_refused VALUES (1);
