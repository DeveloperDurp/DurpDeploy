-- +goose Up
CREATE TABLE agents (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(id) BETWEEN 1 AND 255),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    endpoint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active', 'disabled', 'revoked')),
    agent_version TEXT,
    certificate_pem TEXT,
    certificate_fingerprint TEXT CHECK (length(certificate_fingerprint) = 64),
    encrypted_identity TEXT,
    last_heartbeat_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (status NOT IN ('active', 'disabled') OR
        (certificate_pem IS NOT NULL AND certificate_fingerprint IS NOT NULL
         AND encrypted_identity IS NOT NULL))
);
CREATE UNIQUE INDEX idx_agents_certificate_fingerprint
    ON agents(certificate_fingerprint) WHERE certificate_fingerprint IS NOT NULL;
CREATE INDEX idx_agents_status_heartbeat ON agents(status, last_heartbeat_at);

CREATE TABLE agent_pairings (
    agent_id TEXT PRIMARY KEY NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    pairing_code_hash BLOB NOT NULL UNIQUE CHECK (length(pairing_code_hash) = 32),
    agent_public_identity TEXT NOT NULL,
    agent_pin TEXT NOT NULL UNIQUE CHECK (length(agent_pin) = 64),
    server_public_identity TEXT,
    server_pin TEXT CHECK (length(server_pin) = 64),
    encrypted_identity TEXT,
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'committing', 'paired', 'expired')),
    expires_at INTEGER NOT NULL,
    paired_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK ((state = 'paired' AND paired_at IS NOT NULL
            AND server_public_identity IS NOT NULL AND server_pin IS NOT NULL
            AND encrypted_identity IS NOT NULL)
        OR (state <> 'paired' AND paired_at IS NULL)),
    CHECK (state <> 'committing' OR encrypted_identity IS NOT NULL)
);
CREATE INDEX idx_agent_pairings_state_expires_at
    ON agent_pairings(state, expires_at);

CREATE TABLE environment_agent_assignments (
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE NO ACTION,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (environment_id, agent_id)
);
CREATE INDEX idx_environment_agent_assignments_agent
    ON environment_agent_assignments(agent_id, environment_id);

CREATE TABLE agent_labels (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    label TEXT NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    PRIMARY KEY (agent_id, label)
);
CREATE INDEX idx_agent_labels_label ON agent_labels(label, agent_id);

-- +goose Down
-- Refuse rollback: pairing and agent identities can be referenced by history.
CREATE TABLE remote_agents_rollback_refused (
    guard INTEGER CONSTRAINT remote_agents_history_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_agents_rollback_refused VALUES (1);
