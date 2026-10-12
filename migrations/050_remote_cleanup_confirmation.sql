-- +goose Up
DROP INDEX idx_remote_deployment_claims_claim_token;
DROP INDEX idx_remote_deployment_claims_agent_capacity;
DROP INDEX idx_remote_deployment_claims_waiting;
DROP INDEX idx_remote_deployment_claims_expiry;
DROP INDEX idx_remote_deployment_claims_heartbeat;
ALTER TABLE remote_deployment_claims RENAME TO remote_deployment_claims_old;

CREATE TABLE remote_deployment_claims (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE NO ACTION,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'cancel_requested', 'succeeded',
        'failed', 'cancelled', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed'
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
    log_buffer_ciphertext TEXT,
    cleanup_confirmed_at INTEGER,
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
            AND reason IN ('remote_agent_revoked_before_start',
                'remote_execution_capability_unavailable', 'remote_payload_requires_agent_3'))
        OR (state = 'cancelled' AND finished_at IS NOT NULL
            AND cancel_requested_at IS NOT NULL)
        OR (state = 'cleanup_unconfirmed' AND claim_token_hash IS NOT NULL
            AND started_at IS NOT NULL AND finished_at IS NOT NULL)
        OR (state = 'cancel_unconfirmed' AND claim_token_hash IS NOT NULL
            AND started_at IS NOT NULL AND finished_at IS NOT NULL
            AND cancel_requested_at IS NOT NULL))
);
INSERT INTO remote_deployment_claims (deployment_id, agent_id, state, reason, claim_token_hash, ciphertext, claim_expires_at, last_heartbeat_at, started_at, finished_at, cancel_requested_at, created_at, updated_at, log_buffer_ciphertext)
SELECT deployment_id, agent_id, state, reason, claim_token_hash, ciphertext, claim_expires_at, last_heartbeat_at, started_at, finished_at, cancel_requested_at, created_at, updated_at, log_buffer_ciphertext FROM remote_deployment_claims_old;
DROP TABLE remote_deployment_claims_old;
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

DROP INDEX idx_remote_step_runs_claim_token;
DROP INDEX idx_remote_step_runs_agent_capacity;
DROP INDEX idx_remote_step_runs_waiting;
DROP INDEX idx_remote_step_runs_heartbeat;
ALTER TABLE remote_step_log_sequences RENAME TO remote_step_log_sequences_old;
ALTER TABLE remote_step_runs RENAME TO remote_step_runs_old;

CREATE TABLE remote_step_runs (
    deployment_id INTEGER NOT NULL,
    step_index INTEGER NOT NULL,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'cancel_requested',
        'succeeded', 'failed', 'cancelled', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed'
    )),
    claim_token_hash BLOB CHECK (length(claim_token_hash) = 32),
    ciphertext TEXT,
    claim_expires_at INTEGER,
    last_heartbeat_at INTEGER,
    started_at INTEGER,
    finished_at INTEGER,
    cancel_requested_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    log_buffer_ciphertext TEXT,
    recovery_cancelled INTEGER NOT NULL DEFAULT 0 CHECK (recovery_cancelled IN (0, 1)),
    cleanup_confirmed_at INTEGER,
    PRIMARY KEY (deployment_id, step_index, agent_id),
    FOREIGN KEY (deployment_id, step_index)
        REFERENCES deployment_steps(deployment_id, step_index) ON DELETE NO ACTION
);
INSERT INTO remote_step_runs (deployment_id, step_index, agent_id, state, claim_token_hash, ciphertext, claim_expires_at, last_heartbeat_at, started_at, finished_at, cancel_requested_at, created_at, updated_at, log_buffer_ciphertext, recovery_cancelled)
SELECT deployment_id, step_index, agent_id, state, claim_token_hash, ciphertext, claim_expires_at, last_heartbeat_at, started_at, finished_at, cancel_requested_at, created_at, updated_at, log_buffer_ciphertext, recovery_cancelled FROM remote_step_runs_old;
CREATE UNIQUE INDEX idx_remote_step_runs_claim_token
    ON remote_step_runs(claim_token_hash) WHERE claim_token_hash IS NOT NULL;
CREATE UNIQUE INDEX idx_remote_step_runs_agent_capacity
    ON remote_step_runs(agent_id)
    WHERE state IN ('claimed', 'started', 'cancel_requested');
CREATE INDEX idx_remote_step_runs_waiting
    ON remote_step_runs(agent_id, state, created_at);
CREATE INDEX idx_remote_step_runs_heartbeat
    ON remote_step_runs(state, last_heartbeat_at);

CREATE TABLE remote_step_log_sequences (
    deployment_id INTEGER NOT NULL,
    step_index INTEGER NOT NULL,
    agent_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence >= 0),
    log_id INTEGER NOT NULL REFERENCES deployment_logs(id) ON DELETE NO ACTION,
    PRIMARY KEY (deployment_id, step_index, agent_id, sequence),
    FOREIGN KEY (deployment_id, step_index, agent_id)
        REFERENCES remote_step_runs(deployment_id, step_index, agent_id)
        ON DELETE NO ACTION
);
INSERT INTO remote_step_log_sequences SELECT * FROM remote_step_log_sequences_old;
DROP TABLE remote_step_log_sequences_old;
DROP TABLE remote_step_runs_old;


-- +goose Down
CREATE TABLE remote_cleanup_rollback_refused (guard INTEGER CHECK (guard = 0));
INSERT INTO remote_cleanup_rollback_refused VALUES (1);
