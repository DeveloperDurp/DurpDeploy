-- +goose Up
-- +goose StatementBegin

DROP INDEX idx_agent_pairings_state_expires_at;
ALTER TABLE agent_pairings RENAME TO agent_pairings_legacy;
CREATE TABLE agent_pairings (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE RESTRICT,
    pairing_code_hash BLOB NOT NULL UNIQUE CHECK (length(pairing_code_hash) = 32),
    agent_public_identity TEXT NOT NULL,
    agent_pin TEXT NOT NULL UNIQUE CHECK (length(agent_pin) = 64),
    server_public_identity TEXT,
    server_pin TEXT CHECK (server_pin IS NULL OR length(server_pin) = 64),
    state TEXT NOT NULL CHECK (state IN ('pending', 'committing', 'paired', 'expired')),
    expires_at INTEGER NOT NULL,
    paired_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (
        (state IN ('pending', 'committing', 'expired')
            AND server_public_identity IS NULL AND server_pin IS NULL AND paired_at IS NULL)
        OR (state = 'paired' AND server_public_identity IS NOT NULL
            AND server_pin IS NOT NULL AND paired_at IS NOT NULL)
    )
);
INSERT INTO agent_pairings SELECT * FROM agent_pairings_legacy;
DROP TABLE agent_pairings_legacy;
CREATE INDEX idx_agent_pairings_state_expires_at
    ON agent_pairings(state, expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM agent_pairings WHERE state = 'committing';
DROP INDEX idx_agent_pairings_state_expires_at;
ALTER TABLE agent_pairings RENAME TO agent_pairings_legacy;
CREATE TABLE agent_pairings (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE RESTRICT,
    pairing_code_hash BLOB NOT NULL UNIQUE CHECK (length(pairing_code_hash) = 32),
    agent_public_identity TEXT NOT NULL,
    agent_pin TEXT NOT NULL UNIQUE CHECK (length(agent_pin) = 64),
    server_public_identity TEXT,
    server_pin TEXT CHECK (server_pin IS NULL OR length(server_pin) = 64),
    state TEXT NOT NULL CHECK (state IN ('pending', 'paired', 'expired')),
    expires_at INTEGER NOT NULL,
    paired_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (
        (state = 'pending' AND server_public_identity IS NULL
            AND server_pin IS NULL AND paired_at IS NULL)
        OR (state = 'paired' AND server_public_identity IS NOT NULL
            AND server_pin IS NOT NULL AND paired_at IS NOT NULL)
        OR (state = 'expired' AND server_public_identity IS NULL
            AND server_pin IS NULL AND paired_at IS NULL)
    )
);
INSERT INTO agent_pairings SELECT * FROM agent_pairings_legacy;
DROP TABLE agent_pairings_legacy;
CREATE INDEX idx_agent_pairings_state_expires_at
    ON agent_pairings(state, expires_at);

-- +goose StatementEnd
