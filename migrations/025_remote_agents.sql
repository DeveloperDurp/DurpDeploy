-- +goose Up
-- +goose StatementBegin

ALTER TABLE deployment_logs
    ADD COLUMN sequence INTEGER;
UPDATE deployment_logs SET sequence = id;
CREATE UNIQUE INDEX idx_deployment_logs_deployment_sequence
    ON deployment_logs(deployment_id, sequence)
    WHERE sequence IS NOT NULL;

CREATE TABLE agent_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE UNIQUE INDEX idx_agent_pools_name ON agent_pools(name);

CREATE TABLE agents (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(id) <= 255),
    name TEXT NOT NULL CHECK (length(name) <= 255),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'active', 'disabled', 'revoked'
    )),
    agent_version TEXT,
    certificate_pem TEXT,
    certificate_fingerprint TEXT CHECK (
        certificate_fingerprint IS NULL
        OR length(certificate_fingerprint) = 64
    ),
    last_heartbeat_at INTEGER,
    enrolled_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (
        (status = 'pending' AND certificate_pem IS NULL
            AND certificate_fingerprint IS NULL)
        OR (status IN ('active', 'disabled', 'revoked')
            AND certificate_pem IS NOT NULL
            AND certificate_fingerprint IS NOT NULL)
    )
);
CREATE UNIQUE INDEX idx_agents_certificate_fingerprint
    ON agents(certificate_fingerprint)
    WHERE certificate_fingerprint IS NOT NULL;
CREATE INDEX idx_agents_status ON agents(status);
CREATE INDEX idx_agents_last_heartbeat_at ON agents(last_heartbeat_at);

CREATE TABLE agent_pool_memberships (
    pool_id INTEGER NOT NULL REFERENCES agent_pools(id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (pool_id, agent_id)
);
CREATE INDEX idx_agent_pool_memberships_agent_id
    ON agent_pool_memberships(agent_id);

CREATE TABLE agent_tags (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    tag_key TEXT NOT NULL CHECK (length(tag_key) BETWEEN 1 AND 32),
    tag_value TEXT NOT NULL CHECK (length(tag_value) BETWEEN 1 AND 64),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (agent_id, tag_key)
);
CREATE INDEX idx_agent_tags_agent_id ON agent_tags(agent_id);

CREATE TABLE environment_agent_policies (
    environment_id INTEGER PRIMARY KEY
        REFERENCES environments(id) ON DELETE RESTRICT,
    pool_id INTEGER NOT NULL REFERENCES agent_pools(id) ON DELETE RESTRICT,
    selector TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_environment_agent_policies_pool_id
    ON environment_agent_policies(pool_id);

CREATE TABLE agent_enrollment_tokens (
    token_hash BLOB PRIMARY KEY NOT NULL CHECK (length(token_hash) = 32),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    token_prefix TEXT NOT NULL CHECK (length(token_prefix) <= 16),
    expires_at INTEGER NOT NULL,
    used_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_agent_enrollment_tokens_agent_id
    ON agent_enrollment_tokens(agent_id);
CREATE INDEX idx_agent_enrollment_tokens_expires_at
    ON agent_enrollment_tokens(expires_at);

CREATE TABLE deployment_payloads (
    deployment_id INTEGER PRIMARY KEY
        REFERENCES deployments(id) ON DELETE RESTRICT,
    ciphertext TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE deployment_dispatches (
    deployment_id INTEGER PRIMARY KEY
        REFERENCES deployments(id) ON DELETE RESTRICT,
    mode TEXT NOT NULL CHECK (mode IN ('local', 'remote')),
    pool_id INTEGER REFERENCES agent_pools(id) ON DELETE RESTRICT,
    selector TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'succeeded', 'failed', 'cancelled',
        'lost', 'cancel_requested', 'cancel_unconfirmed'
    )),
    reason TEXT,
    agent_id TEXT REFERENCES agents(id) ON DELETE RESTRICT,
    claim_token_hash BLOB CHECK (
        claim_token_hash IS NULL OR length(claim_token_hash) = 32
    ),
    claim_expires_at INTEGER,
    started_at INTEGER,
    finished_at INTEGER,
    last_heartbeat_at INTEGER,
    cancel_requested_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (
        (mode = 'local' AND pool_id IS NULL)
        OR (mode = 'remote' AND pool_id IS NOT NULL)
    )
);
CREATE INDEX idx_deployment_dispatches_agent_state
    ON deployment_dispatches(agent_id, state);
CREATE UNIQUE INDEX idx_deployment_dispatches_claim_token_hash
    ON deployment_dispatches(claim_token_hash)
    WHERE claim_token_hash IS NOT NULL;
CREATE INDEX idx_deployment_dispatches_state_claim_expires
    ON deployment_dispatches(state, claim_expires_at);

CREATE TABLE agent_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id TEXT REFERENCES agents(id) ON DELETE RESTRICT,
    deployment_id INTEGER REFERENCES deployments(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL CHECK (length(event_type) <= 64),
    dispatch_state TEXT CHECK (dispatch_state IS NULL OR dispatch_state IN (
        'waiting', 'claimed', 'started', 'succeeded', 'failed', 'cancelled',
        'lost', 'cancel_requested', 'cancel_unconfirmed'
    )),
    details TEXT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_agent_events_agent_created_at
    ON agent_events(agent_id, created_at);
CREATE INDEX idx_agent_events_deployment_created_at
    ON agent_events(deployment_id, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Downgrading discards remote-agent configuration, dispatches, payloads, and events.
DROP TABLE agent_events;
DROP TABLE deployment_dispatches;
DROP TABLE deployment_payloads;
DROP TABLE agent_enrollment_tokens;
DROP TABLE environment_agent_policies;
DROP TABLE agent_tags;
DROP TABLE agent_pool_memberships;
DROP TABLE agents;
DROP TABLE agent_pools;
DROP INDEX idx_deployment_logs_deployment_sequence;
ALTER TABLE deployment_logs DROP COLUMN sequence;

-- +goose StatementEnd
