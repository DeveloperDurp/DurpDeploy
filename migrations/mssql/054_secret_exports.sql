-- +goose Up
CREATE TABLE secret_exports (
    id VARCHAR(64) PRIMARY KEY NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id VARCHAR(128) NOT NULL DEFAULT '',
    token_id VARCHAR(128) NOT NULL DEFAULT '',
    claim_hash VARCHAR(64) NOT NULL DEFAULT '',
    scope VARCHAR(16) NOT NULL CHECK (scope IN ('project', 'lifecycle')),
    scope_id BIGINT NOT NULL CHECK (scope_id > 0),
    auth_hash VARCHAR(64) NOT NULL DEFAULT '',
    approved BIGINT NOT NULL DEFAULT 0 CHECK (approved IN (0, 1)),
    expires_at BIGINT NOT NULL
);
CREATE INDEX idx_secret_exports_expiry ON secret_exports(expires_at);

-- +goose Down
DROP TABLE secret_exports;
