-- +goose Up

ALTER TABLE sessions ADD reauthenticated_at BIGINT NULL;

CREATE TABLE mfa_totp (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    encrypted_seed VARBINARY(MAX) NOT NULL,
    last_accepted_step BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);

CREATE TABLE webauthn_users (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    rp_id NVARCHAR(255) NOT NULL,
    user_handle BINARY(32) NOT NULL UNIQUE,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);

CREATE TABLE webauthn_credentials (
    credential_id VARBINARY(900) NOT NULL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name NVARCHAR(255) NOT NULL,
    public_key VARBINARY(MAX) NOT NULL,
    aaguid BINARY(16) NULL,
    transports_json NVARCHAR(MAX) NOT NULL,
    flags BIGINT NOT NULL DEFAULT 0 CHECK (flags >= 0),
    sign_count BIGINT NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
    clone_warning BIGINT NOT NULL DEFAULT 0 CHECK (clone_warning IN (0, 1)),
    attachment NVARCHAR(64) NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CONSTRAINT uq_webauthn_credentials_user_name UNIQUE(user_id, name)
);

CREATE TABLE mfa_recovery_codes (
    id NVARCHAR(255) NOT NULL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash BINARY(32) NOT NULL UNIQUE,
    used_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);

CREATE TABLE mfa_challenges (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- SQL Server rejects cascading user and session paths with error 1785.
    -- Keep this FK at NO ACTION; delete bound challenges before sessions.
    session_id NVARCHAR(255) NULL REFERENCES sessions(id),
    purpose NVARCHAR(64) NOT NULL CHECK (purpose IN (
        'totp_enroll', 'totp_verify', 'webauthn_register', 'webauthn_auth',
        'recovery_verify'
    )),
    csrf_hash BINARY(32) NOT NULL,
    ceremony_json NVARCHAR(MAX) NOT NULL,
    attempts BIGINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    expires_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);

CREATE TABLE mfa_rate_limits (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    window_started_at BIGINT NOT NULL,
    failure_count BIGINT NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    blocked_until BIGINT NULL
);

CREATE INDEX idx_webauthn_credentials_user_id ON webauthn_credentials(user_id);
CREATE INDEX idx_mfa_recovery_codes_user_id ON mfa_recovery_codes(user_id);
CREATE INDEX idx_mfa_challenges_user_expires ON mfa_challenges(user_id, expires_at);
CREATE INDEX idx_mfa_challenges_expires_at ON mfa_challenges(expires_at);


-- +goose Down

DROP TABLE mfa_rate_limits;
DROP TABLE mfa_challenges;
DROP TABLE mfa_recovery_codes;
DROP TABLE webauthn_credentials;
DROP TABLE webauthn_users;
DROP TABLE mfa_totp;
ALTER TABLE sessions DROP COLUMN reauthenticated_at;
