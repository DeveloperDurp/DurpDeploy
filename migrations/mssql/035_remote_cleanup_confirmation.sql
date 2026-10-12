-- +goose Up
ALTER TABLE remote_deployment_claims ADD cleanup_confirmed_at BIGINT NULL;
ALTER TABLE remote_step_runs ADD cleanup_confirmed_at BIGINT NULL;
-- +goose StatementBegin
DECLARE @drop_checks NVARCHAR(MAX) = N'';
SELECT @drop_checks = @drop_checks +
    N'ALTER TABLE remote_deployment_claims DROP CONSTRAINT [' + name + N'];'
FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('remote_deployment_claims')
  AND definition LIKE '%state%';
EXEC sp_executesql @drop_checks;
-- +goose StatementEnd
ALTER TABLE remote_deployment_claims ADD CONSTRAINT ck_remote_claim_state CHECK (
    state IN ('waiting', 'claimed', 'started', 'cancel_requested', 'succeeded',
        'failed', 'cancelled', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed')
);
ALTER TABLE remote_deployment_claims ADD CONSTRAINT ck_remote_claim_lifecycle
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
            AND cancel_requested_at IS NOT NULL));
ALTER TABLE remote_step_runs DROP CONSTRAINT ck_remote_step_runs_state;
ALTER TABLE remote_step_runs ADD CONSTRAINT ck_remote_step_runs_state CHECK (
    state IN ('waiting', 'claimed', 'started', 'cancel_requested',
        'succeeded', 'failed', 'cancelled', 'lost', 'cancel_unconfirmed', 'cleanup_unconfirmed')
);

-- +goose Down
CREATE TABLE remote_cleanup_rollback_refused (guard BIGINT CHECK (guard = 0));
INSERT INTO remote_cleanup_rollback_refused VALUES (1);
