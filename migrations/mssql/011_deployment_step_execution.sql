-- +goose Up
ALTER TABLE steps ADD execution_target NVARCHAR(32) NOT NULL DEFAULT 'local'
    CHECK (execution_target IN ('local', 'agent'));
ALTER TABLE step_templates ADD execution_target NVARCHAR(32) NOT NULL DEFAULT 'local'
    CHECK (execution_target IN ('local', 'agent'));
ALTER TABLE step_template_versions ADD execution_target NVARCHAR(32) NOT NULL DEFAULT 'local'
    CHECK (execution_target IN ('local', 'agent'));

CREATE TABLE step_agent_selectors (
    step_id BIGINT NOT NULL REFERENCES steps(id) ON DELETE CASCADE,
    label NVARCHAR(64) NOT NULL CHECK (LEN(label) BETWEEN 1 AND 64),
    PRIMARY KEY (step_id, label)
);
CREATE TABLE step_template_agent_selectors (
    template_id BIGINT NOT NULL REFERENCES step_templates(id) ON DELETE CASCADE,
    label NVARCHAR(64) NOT NULL CHECK (LEN(label) BETWEEN 1 AND 64),
    PRIMARY KEY (template_id, label)
);
CREATE TABLE step_template_version_agent_selectors (
    template_version_id BIGINT NOT NULL REFERENCES step_template_versions(id) ON DELETE NO ACTION,
    label NVARCHAR(64) NOT NULL CHECK (LEN(label) BETWEEN 1 AND 64),
    PRIMARY KEY (template_version_id, label)
);

-- Preserve legacy JSON byte-for-byte, including malformed historical input.
-- Task 11 materializes per-step rows using this frozen source, never a refreshed release.
CREATE TABLE deployment_step_sources (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE NO ACTION,
    steps_json NVARCHAR(MAX) NOT NULL,
    default_execution_target NVARCHAR(32) NOT NULL DEFAULT 'local'
        CHECK (default_execution_target = 'local'),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
INSERT INTO deployment_step_sources (deployment_id, steps_json)
    SELECT d.id, r.steps_json FROM deployments d JOIN releases r ON r.id = d.release_id;

CREATE TABLE deployment_steps (
    deployment_id BIGINT NOT NULL REFERENCES deployments(id) ON DELETE NO ACTION,
    step_index BIGINT NOT NULL CHECK (step_index >= 0),
    source_step_id BIGINT,
    name NVARCHAR(255) NOT NULL,
    script_body NVARCHAR(MAX) NOT NULL,
    timeout_seconds BIGINT NOT NULL DEFAULT 0 CHECK (timeout_seconds >= 0),
    max_retries BIGINT NOT NULL DEFAULT 0 CHECK (max_retries >= 0),
    execution_target NVARCHAR(32) NOT NULL DEFAULT 'local'
        CHECK (execution_target IN ('local', 'agent')),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (deployment_id, step_index)
);
CREATE TABLE deployment_step_selectors (
    deployment_id BIGINT NOT NULL,
    step_index BIGINT NOT NULL,
    label NVARCHAR(64) NOT NULL CHECK (LEN(label) BETWEEN 1 AND 64),
    PRIMARY KEY (deployment_id, step_index, label),
    FOREIGN KEY (deployment_id, step_index)
        REFERENCES deployment_steps(deployment_id, step_index) ON DELETE NO ACTION
);
CREATE INDEX idx_deployment_step_selectors_label
    ON deployment_step_selectors(label, deployment_id, step_index);

CREATE TABLE deployment_step_attempts (
    deployment_id BIGINT NOT NULL,
    step_index BIGINT NOT NULL,
    attempt BIGINT NOT NULL CHECK (attempt > 0),
    state NVARCHAR(32) NOT NULL DEFAULT 'waiting' CHECK (state IN (
        'waiting', 'claimed', 'started', 'succeeded', 'failed', 'cancelled',
        'lost', 'cancel_requested', 'cancel_uncertain'
    )),
    reason NVARCHAR(MAX),
    agent_id NVARCHAR(255) REFERENCES agents(id) ON DELETE NO ACTION,
    claim_token_hash VARBINARY(32) CHECK (DATALENGTH(claim_token_hash) = 32),
    claim_expires_at BIGINT,
    last_heartbeat_at BIGINT,
    wait_deadline BIGINT NOT NULL,
    started_at BIGINT,
    finished_at BIGINT,
    cancel_requested_at BIGINT,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (deployment_id, step_index, attempt),
    FOREIGN KEY (deployment_id, step_index)
        REFERENCES deployment_steps(deployment_id, step_index) ON DELETE NO ACTION,
    CHECK (state NOT IN ('waiting', 'claimed') OR started_at IS NULL),
    CHECK (state NOT IN ('started', 'succeeded', 'lost') OR started_at IS NOT NULL),
    CHECK ((state IN ('succeeded', 'failed', 'cancelled', 'lost', 'cancel_uncertain')
            AND finished_at IS NOT NULL)
        OR (state IN ('waiting', 'claimed', 'started', 'cancel_requested')
            AND finished_at IS NULL)),
    CHECK (state NOT IN ('cancel_requested', 'cancel_uncertain')
        OR cancel_requested_at IS NOT NULL),
    CHECK ((agent_id IS NULL AND claim_token_hash IS NULL AND claim_expires_at IS NULL)
        OR (agent_id IS NOT NULL AND claim_token_hash IS NOT NULL AND claim_expires_at IS NOT NULL)),
    CHECK (state <> 'claimed' OR agent_id IS NOT NULL)
);
CREATE UNIQUE INDEX idx_deployment_step_attempts_active
    ON deployment_step_attempts(deployment_id, step_index)
    WHERE state IN ('waiting', 'claimed', 'started', 'cancel_requested', 'cancel_uncertain', 'lost');
CREATE INDEX idx_deployment_step_attempts_waiting
    ON deployment_step_attempts(state, wait_deadline, deployment_id, step_index);
CREATE UNIQUE INDEX idx_deployment_step_attempts_claim_token
    ON deployment_step_attempts(claim_token_hash) WHERE claim_token_hash IS NOT NULL;
CREATE UNIQUE INDEX idx_deployment_step_attempts_agent_capacity
    ON deployment_step_attempts(agent_id) WHERE agent_id IS NOT NULL
        AND state IN ('claimed', 'started', 'cancel_requested', 'cancel_uncertain', 'lost');
CREATE INDEX idx_deployment_step_attempts_expiry
    ON deployment_step_attempts(state, claim_expires_at);
CREATE INDEX idx_deployment_step_attempts_heartbeat
    ON deployment_step_attempts(agent_id, last_heartbeat_at);

CREATE TABLE deployment_dispatches (
    deployment_id BIGINT NOT NULL,
    step_index BIGINT NOT NULL,
    attempt BIGINT NOT NULL,
    ciphertext NVARCHAR(MAX),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    updated_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    PRIMARY KEY (deployment_id, step_index, attempt),
    FOREIGN KEY (deployment_id, step_index, attempt)
        REFERENCES deployment_step_attempts(deployment_id, step_index, attempt) ON DELETE NO ACTION
);

-- +goose Down
-- There is no history-preserving conversion back to deployment-wide execution.
CREATE TABLE deployment_steps_rollback_refused (
    guard BIGINT CONSTRAINT deployment_step_history_requires_forward_migration
        CHECK (guard = 0)
);
INSERT INTO deployment_steps_rollback_refused VALUES (1);
