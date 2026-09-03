-- +goose Up

ALTER TABLE deployment_logs
    ADD sequence BIGINT NULL;
UPDATE deployment_logs SET sequence = id;
CREATE UNIQUE INDEX idx_deployment_logs_deployment_sequence
    ON deployment_logs(deployment_id, sequence)
    WHERE sequence IS NOT NULL;

CREATE TABLE agent_pools (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    name NVARCHAR(255) NOT NULL,
    description NVARCHAR(MAX) NULL,
    enabled BIGINT NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE UNIQUE INDEX idx_agent_pools_name ON agent_pools(name);

CREATE TABLE agents (
    id NVARCHAR(255) NOT NULL PRIMARY KEY,
    name NVARCHAR(255) NOT NULL,
    status NVARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'active', 'disabled', 'revoked'
    )),
    agent_version NVARCHAR(255) NULL,
    certificate_pem NVARCHAR(MAX) NULL,
    certificate_fingerprint NVARCHAR(64) NULL CHECK (
        certificate_fingerprint IS NULL
        OR LEN(certificate_fingerprint) = 64
    ),
    last_heartbeat_at BIGINT NULL,
    enrolled_at BIGINT NULL,
    revoked_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
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
    pool_id BIGINT NOT NULL REFERENCES agent_pools(id) ON DELETE NO ACTION,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (pool_id, agent_id)
);
CREATE INDEX idx_agent_pool_memberships_agent_id
    ON agent_pool_memberships(agent_id);

CREATE TABLE agent_tags (
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    tag_key NVARCHAR(32) NOT NULL CHECK (LEN(tag_key) BETWEEN 1 AND 32),
    tag_value NVARCHAR(64) NOT NULL CHECK (LEN(tag_value) BETWEEN 1 AND 64),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (agent_id, tag_key)
);
CREATE INDEX idx_agent_tags_agent_id ON agent_tags(agent_id);

CREATE TABLE environment_agent_policies (
    environment_id BIGINT PRIMARY KEY
        REFERENCES environments(id) ON DELETE NO ACTION,
    pool_id BIGINT NOT NULL REFERENCES agent_pools(id) ON DELETE NO ACTION,
    selector NVARCHAR(MAX) NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE INDEX idx_environment_agent_policies_pool_id
    ON environment_agent_policies(pool_id);

CREATE TABLE agent_enrollment_tokens (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    token_prefix NVARCHAR(16) NOT NULL CHECK (LEN(token_prefix) <= 16),
    expires_at BIGINT NOT NULL,
    used_at BIGINT NULL,
    revoked_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE INDEX idx_agent_enrollment_tokens_agent_id
    ON agent_enrollment_tokens(agent_id);
CREATE INDEX idx_agent_enrollment_tokens_expires_at
    ON agent_enrollment_tokens(expires_at);

CREATE TABLE deployment_payloads (
    deployment_id BIGINT PRIMARY KEY
        REFERENCES deployments(id) ON DELETE NO ACTION,
    ciphertext NVARCHAR(MAX) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);

CREATE TABLE deployment_dispatches (
    deployment_id BIGINT PRIMARY KEY
        REFERENCES deployments(id) ON DELETE NO ACTION,
    mode NVARCHAR(16) NOT NULL CHECK (mode IN ('local', 'remote')),
    pool_id BIGINT NULL REFERENCES agent_pools(id) ON DELETE NO ACTION,
    selector NVARCHAR(MAX) NOT NULL DEFAULT '',
    state NVARCHAR(32) NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'succeeded', 'failed', 'cancelled',
        'lost', 'cancel_requested', 'cancel_unconfirmed'
    )),
    reason NVARCHAR(MAX) NULL,
    agent_id NVARCHAR(255) NULL REFERENCES agents(id) ON DELETE NO ACTION,
    claim_token_hash BINARY(32) NULL,
    claim_expires_at BIGINT NULL,
    started_at BIGINT NULL,
    finished_at BIGINT NULL,
    last_heartbeat_at BIGINT NULL,
    cancel_requested_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
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
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    agent_id NVARCHAR(255) NULL REFERENCES agents(id) ON DELETE NO ACTION,
    deployment_id BIGINT NULL REFERENCES deployments(id) ON DELETE NO ACTION,
    event_type NVARCHAR(64) NOT NULL CHECK (LEN(event_type) <= 64),
    dispatch_state NVARCHAR(32) NULL CHECK (dispatch_state IS NULL OR dispatch_state IN (
        'waiting', 'claimed', 'started', 'succeeded', 'failed', 'cancelled',
        'lost', 'cancel_requested', 'cancel_unconfirmed'
    )),
    details NVARCHAR(MAX) NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE INDEX idx_agent_events_agent_created_at
    ON agent_events(agent_id, created_at);
CREATE INDEX idx_agent_events_deployment_created_at
    ON agent_events(deployment_id, created_at);

-- +goose Down

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
DROP INDEX idx_deployment_logs_deployment_sequence ON deployment_logs;
ALTER TABLE deployment_logs DROP COLUMN sequence;
