-- +goose Up
ALTER TABLE agents ADD deleted_at BIGINT NULL;
ALTER TABLE agents ADD CONSTRAINT ck_agent_deleted_revoked
    CHECK (deleted_at IS NULL OR status = 'revoked');

-- +goose Down
THROW 51000, 'agent deletion requires forward migration', 1;
