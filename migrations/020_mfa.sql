-- +goose Up
-- +goose StatementBegin

ALTER TABLE sessions ADD COLUMN reauthenticated_at INTEGER;

CREATE TABLE mfa_totp (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    encrypted_seed BLOB NOT NULL,
    last_accepted_step INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE webauthn_users (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    rp_id TEXT NOT NULL,
    user_handle BLOB NOT NULL UNIQUE CHECK (length(user_handle) = 32),
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE webauthn_credentials (
    credential_id BLOB PRIMARY KEY NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    public_key BLOB NOT NULL,
    aaguid BLOB CHECK (aaguid IS NULL OR length(aaguid) = 16),
    transports_json TEXT NOT NULL,
    flags INTEGER NOT NULL DEFAULT 0 CHECK (flags >= 0),
    sign_count INTEGER NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
    clone_warning INTEGER NOT NULL DEFAULT 0 CHECK (clone_warning IN (0, 1)),
    attachment TEXT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(user_id, name)
);

CREATE TABLE mfa_recovery_codes (
    id TEXT PRIMARY KEY NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash BLOB NOT NULL UNIQUE CHECK (length(code_hash) = 32),
    used_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE mfa_challenges (
    token_hash BLOB PRIMARY KEY NOT NULL CHECK (length(token_hash) = 32),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN (
        'totp_enroll', 'totp_verify', 'webauthn_register', 'webauthn_auth',
        'recovery_verify'
    )),
    csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
    ceremony_json TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE mfa_rate_limits (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    window_started_at INTEGER NOT NULL,
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    blocked_until INTEGER
);

CREATE INDEX idx_webauthn_credentials_user_id
    ON webauthn_credentials(user_id);
CREATE INDEX idx_mfa_recovery_codes_user_id
    ON mfa_recovery_codes(user_id);
CREATE INDEX idx_mfa_challenges_user_expires
    ON mfa_challenges(user_id, expires_at);
CREATE INDEX idx_mfa_challenges_expires_at ON mfa_challenges(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE mfa_rate_limits;
DROP TABLE mfa_challenges;
DROP TABLE mfa_recovery_codes;
DROP TABLE webauthn_credentials;
DROP TABLE webauthn_users;
DROP TABLE mfa_totp;
ALTER TABLE sessions DROP COLUMN reauthenticated_at;

-- +goose StatementEnd
