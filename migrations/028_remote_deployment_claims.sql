-- +goose Up
ALTER TABLE deployments ADD COLUMN assigned_agent_id TEXT
    REFERENCES agents(id) ON DELETE NO ACTION;
ALTER TABLE agent_pairings ADD COLUMN server_pull_endpoint TEXT;

CREATE TABLE remote_deployment_claims (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE NO ACTION,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'cancel_requested', 'succeeded',
        'failed', 'cancelled', 'lost', 'cancel_unconfirmed'
    )),
    reason TEXT,
    claim_token_hash BLOB CHECK (length(claim_token_hash) = 32),
    ciphertext TEXT,
    claim_expires_at INTEGER,
    last_heartbeat_at INTEGER,
    started_at INTEGER,
    finished_at INTEGER,
    cancel_requested_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
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
    guard INTEGER CONSTRAINT remote_deployment_claim_history_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO remote_deployment_claims_rollback_refused VALUES (1);
