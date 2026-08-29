-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE ambiguous_environment_ids TEXT;
BEGIN
    SELECT string_agg(environment_id::TEXT, ',' ORDER BY environment_id)
    INTO ambiguous_environment_ids
    FROM (
        SELECT DISTINCT deployments.environment_id
        FROM deployment_dispatches
        JOIN deployments ON deployments.id = deployment_dispatches.deployment_id
        LEFT JOIN environment_agent_policies
            ON environment_agent_policies.environment_id = deployments.environment_id
        WHERE deployment_dispatches.mode = 'remote'
           AND deployment_dispatches.assigned_agent_id IS NULL
           AND deployment_dispatches.agent_id IS NULL
           AND (
               deployment_dispatches.state <> 'waiting'
               OR (NOT EXISTS (
                   SELECT 1 FROM environment_agent_assignments
                   JOIN agents ON agents.id = environment_agent_assignments.agent_id
                   WHERE environment_agent_assignments.environment_id = deployments.environment_id
                     AND agents.status = 'active'
               ) AND (
                   (SELECT COUNT(*) FROM agent_pool_memberships
                    WHERE agent_pool_memberships.pool_id = environment_agent_policies.pool_id) <> 1
                   OR (SELECT COUNT(*) FROM agent_pool_memberships
                       JOIN agents ON agents.id = agent_pool_memberships.agent_id
                       WHERE agent_pool_memberships.pool_id = environment_agent_policies.pool_id
                         AND agents.status = 'active') <> 1
               ))
           )
    ) AS ambiguous_environments;
    IF ambiguous_environment_ids IS NOT NULL THEN
        RAISE EXCEPTION 'cannot migrate pooled deployment dispatches for environments: %',
            ambiguous_environment_ids;
    END IF;
END $$;

CREATE TEMP TABLE direct_dispatch_backfill_assignments AS
SELECT deployment_dispatches.deployment_id, COALESCE(
    deployment_dispatches.assigned_agent_id, deployment_dispatches.agent_id,
    CASE WHEN deployment_dispatches.state = 'waiting' THEN (
        SELECT environment_agent_assignments.agent_id
        FROM deployments
        JOIN environment_agent_assignments
            ON environment_agent_assignments.environment_id = deployments.environment_id
        JOIN agents ON agents.id = environment_agent_assignments.agent_id
        WHERE deployments.id = deployment_dispatches.deployment_id
          AND agents.status = 'active'
        ORDER BY environment_agent_assignments.updated_at DESC,
            environment_agent_assignments.agent_id
        LIMIT 1
    ) END,
    (
        SELECT agent_pool_memberships.agent_id
        FROM deployments
        JOIN environment_agent_policies
            ON environment_agent_policies.environment_id = deployments.environment_id
        JOIN agent_pool_memberships
            ON agent_pool_memberships.pool_id = environment_agent_policies.pool_id
        JOIN agents ON agents.id = agent_pool_memberships.agent_id
        WHERE deployments.id = deployment_dispatches.deployment_id
          AND agents.status = 'active'
        ORDER BY agent_pool_memberships.created_at DESC, agent_pool_memberships.agent_id
        LIMIT 1
    )
) AS assigned_agent_id
FROM deployment_dispatches
WHERE mode = 'remote';

CREATE TABLE direct_dispatch_backup_deployment_dispatches AS
    SELECT * FROM deployment_dispatches;

DROP INDEX idx_deployment_dispatches_active_agent;
DROP INDEX idx_deployment_dispatches_assigned_agent_state;
DROP INDEX idx_deployment_dispatches_agent_state;
DROP INDEX idx_deployment_dispatches_claim_token_hash;
DROP INDEX idx_deployment_dispatches_state_claim_expires;
ALTER TABLE deployment_dispatches RENAME TO deployment_dispatches_legacy;
CREATE TABLE deployment_dispatches (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE RESTRICT,
    mode TEXT NOT NULL CHECK (mode IN ('local', 'remote')),
    state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting', 'claimed', 'started', 'succeeded', 'failed', 'cancelled', 'lost', 'cancel_requested', 'cancel_unconfirmed')),
    reason TEXT,
    agent_id TEXT REFERENCES agents(id) ON DELETE RESTRICT,
    assigned_agent_id TEXT REFERENCES agents(id) ON DELETE RESTRICT,
    claim_token_hash BYTEA CHECK (claim_token_hash IS NULL OR length(claim_token_hash) = 32),
    claim_expires_at BIGINT,
    started_at BIGINT,
    finished_at BIGINT,
    last_heartbeat_at BIGINT,
    cancel_requested_at BIGINT,
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW()),
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW()),
    CONSTRAINT ck_deployment_dispatches_mode_assignment CHECK ((mode = 'local' AND assigned_agent_id IS NULL) OR (mode = 'remote' AND assigned_agent_id IS NOT NULL))
);
INSERT INTO deployment_dispatches (
    deployment_id, mode, state, reason, agent_id, assigned_agent_id,
    claim_token_hash, claim_expires_at, started_at, finished_at,
    last_heartbeat_at, cancel_requested_at, created_at, updated_at
)
SELECT legacy.deployment_id, legacy.mode, legacy.state, legacy.reason, legacy.agent_id,
    COALESCE(legacy.assigned_agent_id, legacy.agent_id, assignments.assigned_agent_id),
    legacy.claim_token_hash, legacy.claim_expires_at, legacy.started_at,
    legacy.finished_at, legacy.last_heartbeat_at, legacy.cancel_requested_at,
    legacy.created_at, legacy.updated_at
FROM deployment_dispatches_legacy AS legacy
LEFT JOIN direct_dispatch_backfill_assignments AS assignments
    ON assignments.deployment_id = legacy.deployment_id;
DROP TABLE direct_dispatch_backfill_assignments;
DROP TABLE deployment_dispatches_legacy;
CREATE INDEX idx_deployment_dispatches_agent_state ON deployment_dispatches(agent_id, state);
CREATE UNIQUE INDEX idx_deployment_dispatches_claim_token_hash
    ON deployment_dispatches(claim_token_hash) WHERE claim_token_hash IS NOT NULL;
CREATE INDEX idx_deployment_dispatches_state_claim_expires
    ON deployment_dispatches(state, claim_expires_at);
CREATE INDEX idx_deployment_dispatches_assigned_agent_state
    ON deployment_dispatches(assigned_agent_id, state);
CREATE UNIQUE INDEX idx_deployment_dispatches_active_agent
    ON deployment_dispatches(agent_id)
    WHERE agent_id IS NOT NULL AND state IN ('claimed', 'started', 'cancel_requested');
CREATE TABLE direct_dispatch_backup_agent_pools AS SELECT * FROM agent_pools;
CREATE TABLE direct_dispatch_backup_sequences AS
    SELECT last_value, is_called FROM agent_pools_id_seq;
CREATE TABLE direct_dispatch_backup_agent_pool_memberships AS
    SELECT * FROM agent_pool_memberships;
CREATE TABLE direct_dispatch_backup_agent_tags AS SELECT * FROM agent_tags;
CREATE TABLE direct_dispatch_backup_environment_agent_policies AS
    SELECT * FROM environment_agent_policies;
DROP TABLE environment_agent_policies;
DROP TABLE agent_tags;
DROP TABLE agent_pool_memberships;
DROP TABLE agent_pools;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE agent_pools (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW()),
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())
);
CREATE UNIQUE INDEX idx_agent_pools_name ON agent_pools(name);
CREATE TABLE agent_pool_memberships (
    pool_id BIGINT NOT NULL REFERENCES agent_pools(id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW()),
    PRIMARY KEY (pool_id, agent_id)
);
CREATE INDEX idx_agent_pool_memberships_agent_id ON agent_pool_memberships(agent_id);
CREATE TABLE agent_tags (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    tag_key TEXT NOT NULL CHECK (length(tag_key) BETWEEN 1 AND 32),
    tag_value TEXT NOT NULL CHECK (length(tag_value) BETWEEN 1 AND 64),
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW()),
    PRIMARY KEY (agent_id, tag_key)
);
CREATE INDEX idx_agent_tags_agent_id ON agent_tags(agent_id);
CREATE TABLE environment_agent_policies (
    environment_id BIGINT PRIMARY KEY REFERENCES environments(id) ON DELETE RESTRICT,
    pool_id BIGINT NOT NULL REFERENCES agent_pools(id) ON DELETE RESTRICT,
    selector TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW()),
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())
);
CREATE INDEX idx_environment_agent_policies_pool_id ON environment_agent_policies(pool_id);
INSERT INTO agent_pools (name)
SELECT agent_id FROM environment_agent_assignments
UNION SELECT assigned_agent_id FROM deployment_dispatches
WHERE assigned_agent_id IS NOT NULL;
INSERT INTO agent_pool_memberships (pool_id, agent_id)
SELECT agent_pools.id, agent_sources.agent_id
FROM (
    SELECT agent_id FROM environment_agent_assignments
    UNION SELECT assigned_agent_id FROM deployment_dispatches WHERE assigned_agent_id IS NOT NULL
) AS agent_sources
JOIN agent_pools ON agent_pools.name = agent_sources.agent_id;
INSERT INTO environment_agent_policies (environment_id, pool_id)
SELECT environment_agent_assignments.environment_id, agent_pools.id
FROM environment_agent_assignments
JOIN agent_pools ON agent_pools.name = environment_agent_assignments.agent_id;
ALTER TABLE deployment_dispatches DROP CONSTRAINT ck_deployment_dispatches_mode_assignment;
ALTER TABLE deployment_dispatches ADD pool_id BIGINT REFERENCES agent_pools(id) ON DELETE RESTRICT;
ALTER TABLE deployment_dispatches ADD selector TEXT NOT NULL DEFAULT '';
UPDATE deployment_dispatches
SET pool_id = agent_pool_memberships.pool_id, assigned_agent_id = NULL
FROM agent_pool_memberships
WHERE agent_pool_memberships.agent_id = deployment_dispatches.assigned_agent_id
  AND deployment_dispatches.mode = 'remote';
DELETE FROM deployment_dispatches;
DELETE FROM environment_agent_policies;
DELETE FROM agent_tags;
DELETE FROM agent_pool_memberships;
DELETE FROM agent_pools;
INSERT INTO agent_pools SELECT * FROM direct_dispatch_backup_agent_pools;
SELECT setval('agent_pools_id_seq', last_value, is_called)
FROM direct_dispatch_backup_sequences;
INSERT INTO agent_pool_memberships
    SELECT * FROM direct_dispatch_backup_agent_pool_memberships;
INSERT INTO agent_tags SELECT * FROM direct_dispatch_backup_agent_tags;
INSERT INTO environment_agent_policies
    SELECT * FROM direct_dispatch_backup_environment_agent_policies;
INSERT INTO deployment_dispatches (
    deployment_id, mode, pool_id, selector, state, reason, agent_id,
    assigned_agent_id, claim_token_hash, claim_expires_at, started_at,
    finished_at, last_heartbeat_at, cancel_requested_at, created_at, updated_at
)
SELECT
    deployment_id, mode, pool_id, selector, state, reason, agent_id,
    assigned_agent_id, claim_token_hash::bytea, claim_expires_at, started_at,
    finished_at, last_heartbeat_at, cancel_requested_at, created_at, updated_at
FROM direct_dispatch_backup_deployment_dispatches;
DROP TABLE direct_dispatch_backup_deployment_dispatches;
DROP TABLE direct_dispatch_backup_agent_pools;
DROP TABLE direct_dispatch_backup_sequences;
DROP TABLE direct_dispatch_backup_agent_pool_memberships;
DROP TABLE direct_dispatch_backup_agent_tags;
DROP TABLE direct_dispatch_backup_environment_agent_policies;
ALTER TABLE deployment_dispatches ADD CONSTRAINT ck_deployment_dispatches_mode_assignment
CHECK (
    (mode = 'local' AND pool_id IS NULL AND assigned_agent_id IS NULL)
    OR (mode = 'remote' AND (
        (pool_id IS NOT NULL AND assigned_agent_id IS NULL)
        OR (pool_id IS NULL AND assigned_agent_id IS NOT NULL)
    ))
);

-- +goose StatementEnd
