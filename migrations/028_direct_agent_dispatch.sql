-- +goose Up
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
        (mode = 'local' AND assigned_agent_id IS NULL)
        OR (mode = 'remote' AND assigned_agent_id IS NOT NULL)
    )
);
INSERT INTO deployment_dispatches (
    deployment_id, mode, state, reason, agent_id, assigned_agent_id,
    claim_token_hash, claim_expires_at, started_at, finished_at,
    last_heartbeat_at, cancel_requested_at, created_at, updated_at
)
SELECT
    deployment_id, mode, state, reason, agent_id,
    COALESCE(assigned_agent_id, agent_id), claim_token_hash, claim_expires_at,
    started_at, finished_at, last_heartbeat_at, cancel_requested_at,
    created_at, updated_at
FROM deployment_dispatches_legacy
WHERE mode = 'local' OR assigned_agent_id IS NOT NULL OR agent_id IS NOT NULL;
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

DROP TABLE environment_agent_policies;
DROP TABLE agent_tags;
DROP TABLE agent_pool_memberships;
DROP TABLE agent_pools;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE agent_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE UNIQUE INDEX idx_agent_pools_name ON agent_pools(name);
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
    environment_id INTEGER PRIMARY KEY REFERENCES environments(id) ON DELETE RESTRICT,
    pool_id INTEGER NOT NULL REFERENCES agent_pools(id) ON DELETE RESTRICT,
    selector TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_environment_agent_policies_pool_id
    ON environment_agent_policies(pool_id);

INSERT INTO agent_pools (name)
SELECT agent_id
FROM environment_agent_assignments
UNION
SELECT assigned_agent_id
FROM deployment_dispatches
WHERE assigned_agent_id IS NOT NULL
ON CONFLICT(name) DO NOTHING;
INSERT INTO agent_pool_memberships (pool_id, agent_id)
SELECT agent_pools.id, agent_sources.agent_id
FROM (
    SELECT agent_id FROM environment_agent_assignments
    UNION
    SELECT assigned_agent_id FROM deployment_dispatches
    WHERE assigned_agent_id IS NOT NULL
) AS agent_sources
JOIN agent_pools ON agent_pools.name = agent_sources.agent_id
ON CONFLICT(pool_id, agent_id) DO NOTHING;
INSERT INTO environment_agent_policies (environment_id, pool_id)
SELECT environment_agent_assignments.environment_id, agent_pools.id
FROM environment_agent_assignments
JOIN agent_pools
    ON agent_pools.name = environment_agent_assignments.agent_id
ON CONFLICT(environment_id) DO UPDATE SET pool_id = excluded.pool_id;

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
    claim_token_hash,
    claim_expires_at, started_at, finished_at, last_heartbeat_at,
    cancel_requested_at, created_at, updated_at
)
SELECT
    deployment_id, mode,
    CASE
        WHEN mode = 'remote' THEN (
            SELECT agent_pool_memberships.pool_id
            FROM agent_pool_memberships
            WHERE agent_pool_memberships.agent_id = assigned_agent_id
        )
    END,
    '', state, reason, agent_id, claim_token_hash,
    claim_expires_at, started_at, finished_at, last_heartbeat_at,
    cancel_requested_at, created_at, updated_at
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
