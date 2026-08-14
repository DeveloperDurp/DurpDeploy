-- +goose Up
-- +goose StatementBegin

CREATE TABLE mfa_challenges_next (
    token_hash BLOB PRIMARY KEY NOT NULL CHECK (length(token_hash) = 32),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN (
        'login_mfa', 'totp_enroll', 'totp_verify', 'webauthn_register',
        'webauthn_auth', 'recovery_verify', 'admin_mfa_reset'
    )),
    csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
    ceremony_json TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

INSERT INTO mfa_challenges_next
SELECT * FROM mfa_challenges;
DROP TABLE mfa_challenges;
ALTER TABLE mfa_challenges_next RENAME TO mfa_challenges;
CREATE INDEX idx_mfa_challenges_user_expires
    ON mfa_challenges(user_id, expires_at);
CREATE INDEX idx_mfa_challenges_expires_at ON mfa_challenges(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM mfa_challenges WHERE purpose = 'admin_mfa_reset';
CREATE TABLE mfa_challenges_previous (
    token_hash BLOB PRIMARY KEY NOT NULL CHECK (length(token_hash) = 32),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN (
        'login_mfa', 'totp_enroll', 'totp_verify', 'webauthn_register',
        'webauthn_auth', 'recovery_verify'
    )),
    csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
    ceremony_json TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

INSERT INTO mfa_challenges_previous
SELECT * FROM mfa_challenges;
DROP TABLE mfa_challenges;
ALTER TABLE mfa_challenges_previous RENAME TO mfa_challenges;
CREATE INDEX idx_mfa_challenges_user_expires
    ON mfa_challenges(user_id, expires_at);
CREATE INDEX idx_mfa_challenges_expires_at ON mfa_challenges(expires_at);

-- +goose StatementEnd
