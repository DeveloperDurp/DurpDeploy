-- +goose Up
CREATE TABLE secret_exports (
    id TEXT PRIMARY KEY NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL DEFAULT '',
    token_id TEXT NOT NULL DEFAULT '',
    claim_hash TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL CHECK (scope IN ('project', 'lifecycle')),
    scope_id INTEGER NOT NULL CHECK (scope_id > 0),
    auth_hash TEXT NOT NULL DEFAULT '',
    approved INTEGER NOT NULL DEFAULT 0 CHECK (approved IN (0, 1)),
    expires_at INTEGER NOT NULL
);
CREATE INDEX idx_secret_exports_expiry ON secret_exports(expires_at);

-- +goose Down
DROP TABLE secret_exports;
