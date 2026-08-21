-- +goose Up
-- +goose StatementBegin

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
CREATE INDEX idx_agent_pairings_state_expires_at
    ON agent_pairings(state, expires_at);

CREATE TABLE environment_agent_assignments (
    environment_id INTEGER PRIMARY KEY
        REFERENCES environments(id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_environment_agent_assignments_agent_id
    ON environment_agent_assignments(agent_id);

DROP INDEX idx_deployment_dispatches_agent_state;
DROP INDEX idx_deployment_dispatches_claim_token_hash;
DROP INDEX idx_deployment_dispatches_state_claim_expires;
ALTER TABLE deployment_dispatches RENAME TO deployment_dispatches_legacy;
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
    assigned_agent_id TEXT REFERENCES agents(id) ON DELETE RESTRICT,
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
        (mode = 'local' AND pool_id IS NULL AND assigned_agent_id IS NULL)
        OR (mode = 'remote' AND (
            (pool_id IS NOT NULL AND assigned_agent_id IS NULL)
            OR (pool_id IS NULL AND assigned_agent_id IS NOT NULL)
        ))
    )
);
INSERT INTO deployment_dispatches (
    deployment_id, mode, pool_id, selector, state, reason, agent_id,
    claim_token_hash, claim_expires_at, started_at, finished_at,
    last_heartbeat_at, cancel_requested_at, created_at, updated_at
)
SELECT
    deployment_id, mode, pool_id, selector, state, reason, agent_id,
    claim_token_hash, claim_expires_at, started_at, finished_at,
    last_heartbeat_at, cancel_requested_at, created_at, updated_at
FROM deployment_dispatches_legacy;
DROP TABLE deployment_dispatches_legacy;
CREATE INDEX idx_deployment_dispatches_agent_state
    ON deployment_dispatches(agent_id, state);
CREATE UNIQUE INDEX idx_deployment_dispatches_claim_token_hash
    ON deployment_dispatches(claim_token_hash)
    WHERE claim_token_hash IS NOT NULL;
CREATE INDEX idx_deployment_dispatches_state_claim_expires
    ON deployment_dispatches(state, claim_expires_at);
CREATE INDEX idx_deployment_dispatches_assigned_agent_state
    ON deployment_dispatches(assigned_agent_id, state);
CREATE UNIQUE INDEX idx_deployment_dispatches_active_agent
    ON deployment_dispatches(agent_id)
    WHERE agent_id IS NOT NULL
      AND state IN ('claimed', 'started', 'cancel_requested');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX idx_deployment_dispatches_active_agent;
DROP INDEX idx_deployment_dispatches_assigned_agent_state;
DROP INDEX idx_deployment_dispatches_agent_state;
DROP INDEX idx_deployment_dispatches_claim_token_hash;
DROP INDEX idx_deployment_dispatches_state_claim_expires;
ALTER TABLE deployment_dispatches RENAME TO deployment_dispatches_legacy;
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
INSERT INTO deployment_dispatches (
    deployment_id, mode, pool_id, selector, state, reason, agent_id,
    claim_token_hash, claim_expires_at, started_at, finished_at,
    last_heartbeat_at, cancel_requested_at, created_at, updated_at
)
SELECT
    deployment_id, mode, pool_id, selector, state, reason, agent_id,
    claim_token_hash, claim_expires_at, started_at, finished_at,
    last_heartbeat_at, cancel_requested_at, created_at, updated_at
FROM deployment_dispatches_legacy
WHERE assigned_agent_id IS NULL;
DROP TABLE deployment_dispatches_legacy;
CREATE INDEX idx_deployment_dispatches_agent_state
    ON deployment_dispatches(agent_id, state);
CREATE UNIQUE INDEX idx_deployment_dispatches_claim_token_hash
    ON deployment_dispatches(claim_token_hash)
    WHERE claim_token_hash IS NOT NULL;
CREATE INDEX idx_deployment_dispatches_state_claim_expires
    ON deployment_dispatches(state, claim_expires_at);
DROP TABLE environment_agent_assignments;
DROP TABLE agent_pairings;

-- +goose StatementEnd
