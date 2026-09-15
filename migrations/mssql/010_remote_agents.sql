-- +goose Up
CREATE TABLE agents (
    id NVARCHAR(255) PRIMARY KEY NOT NULL CHECK (LEN(id) BETWEEN 1 AND 255),
    name NVARCHAR(255) NOT NULL CHECK (LEN(name) BETWEEN 1 AND 255),
    endpoint NVARCHAR(MAX) NOT NULL,
    status NVARCHAR(32) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active', 'disabled', 'revoked')),
    agent_version NVARCHAR(MAX),
    certificate_pem NVARCHAR(MAX),
    certificate_fingerprint NVARCHAR(64) CHECK (LEN(certificate_fingerprint) = 64),
    encrypted_identity NVARCHAR(MAX),
    last_heartbeat_at BIGINT,
    revoked_at BIGINT,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CHECK (status NOT IN ('active', 'disabled') OR
        (certificate_pem IS NOT NULL AND certificate_fingerprint IS NOT NULL
         AND encrypted_identity IS NOT NULL))
);
CREATE UNIQUE INDEX idx_agents_certificate_fingerprint
    ON agents(certificate_fingerprint) WHERE certificate_fingerprint IS NOT NULL;
CREATE INDEX idx_agents_status_heartbeat ON agents(status, last_heartbeat_at);

CREATE TABLE agent_pairings (
    agent_id NVARCHAR(255) PRIMARY KEY NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    pairing_code_hash VARBINARY(32) NOT NULL UNIQUE CHECK (DATALENGTH(pairing_code_hash) = 32),
    agent_public_identity NVARCHAR(MAX) NOT NULL,
    agent_pin NVARCHAR(64) NOT NULL UNIQUE CHECK (LEN(agent_pin) = 64),
    server_public_identity NVARCHAR(MAX),
    server_pin NVARCHAR(64) CHECK (LEN(server_pin) = 64),
    encrypted_identity NVARCHAR(MAX),
    state NVARCHAR(32) NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'committing', 'paired', 'expired')),
    expires_at BIGINT NOT NULL,
    paired_at BIGINT,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CHECK ((state = 'paired' AND paired_at IS NOT NULL
            AND server_public_identity IS NOT NULL AND server_pin IS NOT NULL
            AND encrypted_identity IS NOT NULL)
        OR (state <> 'paired' AND paired_at IS NULL)),
    CHECK (state <> 'committing' OR encrypted_identity IS NOT NULL)
);
CREATE INDEX idx_agent_pairings_state_expires_at
    ON agent_pairings(state, expires_at);

CREATE TABLE environment_agent_assignments (
    environment_id BIGINT NOT NULL REFERENCES environments(id) ON DELETE NO ACTION,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (environment_id, agent_id)
);
CREATE INDEX idx_environment_agent_assignments_agent
    ON environment_agent_assignments(agent_id, environment_id);

CREATE TABLE agent_labels (
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    label NVARCHAR(64) NOT NULL CHECK (LEN(label) BETWEEN 1 AND 64),
    PRIMARY KEY (agent_id, label)
);
CREATE INDEX idx_agent_labels_label ON agent_labels(label, agent_id);

-- +goose Down
-- Refuse rollback: pairing and agent identities can be referenced by history.
CREATE TABLE remote_agents_rollback_refused (
    guard BIGINT CONSTRAINT remote_agents_history_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_agents_rollback_refused VALUES (1);
