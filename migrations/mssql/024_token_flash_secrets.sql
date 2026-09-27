-- +goose Up
CREATE TABLE token_flash_secrets (
    id NVARCHAR(255) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id NVARCHAR(255) NOT NULL,
    token_value NVARCHAR(MAX) NOT NULL,
    token_name NVARCHAR(255) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND,'1970-01-01',SYSUTCDATETIME()),
    expires_at BIGINT NOT NULL
);
CREATE INDEX idx_token_flash_secrets_expires_at
    ON token_flash_secrets(expires_at);

-- +goose Down
CREATE TABLE token_flash_secrets_rollback_refused (
    guard BIGINT CONSTRAINT token_flash_secrets_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO token_flash_secrets_rollback_refused VALUES (1);
