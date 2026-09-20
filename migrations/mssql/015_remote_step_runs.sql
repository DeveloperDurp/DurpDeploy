-- +goose Up
CREATE TABLE remote_step_runs (
    deployment_id BIGINT NOT NULL,
    step_index BIGINT NOT NULL,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id),
    state NVARCHAR(32) NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'cancel_requested',
        'succeeded', 'failed', 'cancelled'
    )),
    claim_token_hash VARBINARY(32),
    ciphertext NVARCHAR(MAX),
    claim_expires_at BIGINT,
    last_heartbeat_at BIGINT,
    started_at BIGINT,
    finished_at BIGINT,
    cancel_requested_at BIGINT,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND,'1970-01-01',SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND,'1970-01-01',SYSUTCDATETIME()),
    PRIMARY KEY (deployment_id, step_index, agent_id),
    FOREIGN KEY (deployment_id, step_index)
        REFERENCES deployment_steps(deployment_id, step_index)
);
CREATE UNIQUE INDEX idx_remote_step_runs_claim_token
    ON remote_step_runs(claim_token_hash) WHERE claim_token_hash IS NOT NULL;
CREATE UNIQUE INDEX idx_remote_step_runs_agent_capacity
    ON remote_step_runs(agent_id)
    WHERE state IN ('claimed', 'started', 'cancel_requested');
CREATE INDEX idx_remote_step_runs_waiting
    ON remote_step_runs(agent_id, state, created_at);

CREATE TABLE remote_step_log_sequences (
    deployment_id BIGINT NOT NULL,
    step_index BIGINT NOT NULL,
    agent_id NVARCHAR(255) NOT NULL,
    sequence BIGINT NOT NULL CHECK (sequence >= 0),
    log_id BIGINT NOT NULL REFERENCES deployment_logs(id),
    PRIMARY KEY (deployment_id, step_index, agent_id, sequence),
    FOREIGN KEY (deployment_id, step_index, agent_id)
        REFERENCES remote_step_runs(deployment_id, step_index, agent_id)
);

-- +goose Down
DROP TABLE remote_step_log_sequences;
DROP TABLE remote_step_runs;
