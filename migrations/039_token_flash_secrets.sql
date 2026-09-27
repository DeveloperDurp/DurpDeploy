-- +goose Up
-- +goose StatementBegin

CREATE TABLE token_flash_secrets (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    token_value TEXT NOT NULL,
    token_name TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at INTEGER NOT NULL
);

CREATE INDEX idx_token_flash_secrets_expires_at
    ON token_flash_secrets(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE token_flash_secrets_rollback_refused (
    guard INTEGER CONSTRAINT token_flash_secrets_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO token_flash_secrets_rollback_refused VALUES (1);

-- +goose StatementEnd
