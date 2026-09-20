-- +goose Up
CREATE TABLE remote_step_runs (
    deployment_id INTEGER NOT NULL,
    step_index INTEGER NOT NULL,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'cancel_requested',
        'succeeded', 'failed', 'cancelled'
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
    PRIMARY KEY (deployment_id, step_index, agent_id),
    FOREIGN KEY (deployment_id, step_index)
        REFERENCES deployment_steps(deployment_id, step_index) ON DELETE NO ACTION
);
CREATE UNIQUE INDEX idx_remote_step_runs_claim_token
    ON remote_step_runs(claim_token_hash) WHERE claim_token_hash IS NOT NULL;
CREATE UNIQUE INDEX idx_remote_step_runs_agent_capacity
    ON remote_step_runs(agent_id)
    WHERE state IN ('claimed', 'started', 'cancel_requested');
CREATE INDEX idx_remote_step_runs_waiting
    ON remote_step_runs(agent_id, state, created_at);

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

-- +goose Down
DROP TABLE remote_step_log_sequences;
DROP TABLE remote_step_runs;
