-- +goose Up
CREATE UNIQUE INDEX idx_environment_agent_assignments_environment
    ON environment_agent_assignments(environment_id);

ALTER TABLE deployments ADD assigned_agent_id NVARCHAR(255) NULL;
ALTER TABLE deployments ADD CONSTRAINT fk_deployments_assigned_agent
    FOREIGN KEY (assigned_agent_id) REFERENCES agents(id) ON DELETE NO ACTION;
ALTER TABLE agent_pairings ADD server_pull_endpoint NVARCHAR(MAX) NULL;

CREATE TABLE remote_deployment_claims (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE NO ACTION,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    state NVARCHAR(32) NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'cancel_requested', 'succeeded',
        'failed', 'cancelled', 'lost', 'cancel_unconfirmed'
    )),
    reason NVARCHAR(MAX),
    claim_token_hash VARBINARY(32) CHECK (DATALENGTH(claim_token_hash) = 32),
    ciphertext NVARCHAR(MAX),
    claim_expires_at BIGINT,
    last_heartbeat_at BIGINT,
    started_at BIGINT,
    finished_at BIGINT,
    cancel_requested_at BIGINT,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CHECK ((claim_token_hash IS NULL AND ciphertext IS NULL
            AND claim_expires_at IS NULL AND last_heartbeat_at IS NULL)
        OR (claim_token_hash IS NOT NULL AND ciphertext IS NOT NULL
            AND claim_expires_at IS NOT NULL AND last_heartbeat_at IS NOT NULL)),
    CHECK ((state = 'waiting' AND claim_token_hash IS NULL
            AND started_at IS NULL AND finished_at IS NULL
            AND cancel_requested_at IS NULL)
        OR (state = 'claimed' AND claim_token_hash IS NOT NULL
            AND started_at IS NULL AND finished_at IS NULL
            AND cancel_requested_at IS NULL)
        OR (state = 'started' AND claim_token_hash IS NOT NULL
            AND started_at IS NOT NULL AND finished_at IS NULL
            AND cancel_requested_at IS NULL)
        OR (state = 'cancel_requested' AND claim_token_hash IS NOT NULL
            AND started_at IS NOT NULL AND finished_at IS NULL
            AND cancel_requested_at IS NOT NULL)
        OR (state IN ('succeeded', 'failed', 'lost')
            AND claim_token_hash IS NOT NULL AND started_at IS NOT NULL
            AND finished_at IS NOT NULL AND cancel_requested_at IS NULL)
        OR (state = 'failed' AND started_at IS NULL
            AND finished_at IS NOT NULL AND cancel_requested_at IS NULL
            AND reason = 'remote_agent_revoked_before_start')
        OR (state = 'cancelled' AND finished_at IS NOT NULL
            AND cancel_requested_at IS NOT NULL)
        OR (state = 'cancel_unconfirmed' AND claim_token_hash IS NOT NULL
            AND started_at IS NOT NULL AND finished_at IS NOT NULL
            AND cancel_requested_at IS NOT NULL))
);
CREATE UNIQUE INDEX idx_remote_deployment_claims_claim_token
    ON remote_deployment_claims(claim_token_hash)
    WHERE claim_token_hash IS NOT NULL;
CREATE UNIQUE INDEX idx_remote_deployment_claims_agent_capacity
    ON remote_deployment_claims(agent_id)
    WHERE state IN ('claimed', 'started', 'cancel_requested');
CREATE INDEX idx_remote_deployment_claims_waiting
    ON remote_deployment_claims(agent_id, state, created_at, deployment_id);
CREATE INDEX idx_remote_deployment_claims_expiry
    ON remote_deployment_claims(state, claim_expires_at);
CREATE INDEX idx_remote_deployment_claims_heartbeat
    ON remote_deployment_claims(agent_id, state, last_heartbeat_at);

-- +goose Down
-- Claims and snapshotted routing are deployment history and cannot be removed safely.
CREATE TABLE remote_deployment_claims_rollback_refused (
    guard BIGINT CONSTRAINT remote_deployment_claim_history_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_deployment_claims_rollback_refused VALUES (1);
