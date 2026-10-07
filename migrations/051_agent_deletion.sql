-- +goose Up
ALTER TABLE agents ADD COLUMN deleted_at INTEGER
    CHECK (deleted_at IS NULL OR status = 'revoked');

-- +goose Down
ALTER TABLE agents DROP COLUMN deleted_at;
