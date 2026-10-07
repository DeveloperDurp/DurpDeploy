-- +goose Up
ALTER TABLE agents ADD deleted_at BIGINT NULL
    CONSTRAINT ck_agent_deleted_revoked
    CHECK (deleted_at IS NULL OR status = 'revoked');

-- +goose Down
ALTER TABLE agents DROP CONSTRAINT ck_agent_deleted_revoked;
ALTER TABLE agents DROP COLUMN deleted_at;
