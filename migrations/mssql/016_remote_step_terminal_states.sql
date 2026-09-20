-- +goose Up
-- +goose StatementBegin
DECLARE @old_constraint NVARCHAR(128);
SELECT @old_constraint = name
FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('remote_step_runs')
  AND definition LIKE '%state%';
EXEC('ALTER TABLE remote_step_runs DROP CONSTRAINT [' + @old_constraint + ']');
-- +goose StatementEnd
ALTER TABLE remote_step_runs ADD CONSTRAINT ck_remote_step_runs_state CHECK (
    state IN ('waiting', 'claimed', 'started', 'cancel_requested',
              'succeeded', 'failed', 'cancelled', 'lost', 'cancel_unconfirmed')
);
CREATE INDEX idx_remote_step_runs_heartbeat
    ON remote_step_runs(state, last_heartbeat_at);

-- +goose Down
DROP INDEX idx_remote_step_runs_heartbeat ON remote_step_runs;
UPDATE remote_step_runs SET state = 'failed'
WHERE state IN ('lost', 'cancel_unconfirmed');
ALTER TABLE remote_step_runs DROP CONSTRAINT ck_remote_step_runs_state;
ALTER TABLE remote_step_runs ADD CONSTRAINT ck_remote_step_runs_state CHECK (
    state IN ('waiting', 'claimed', 'started', 'cancel_requested',
              'succeeded', 'failed', 'cancelled')
);
