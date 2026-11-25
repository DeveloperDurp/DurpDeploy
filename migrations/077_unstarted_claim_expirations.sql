-- +goose Up
ALTER TABLE remote_step_runs ADD COLUMN expired_claims INTEGER NOT NULL DEFAULT 0;
ALTER TABLE remote_step_runs ADD COLUMN prestart_input INTEGER NOT NULL DEFAULT 0;
UPDATE remote_step_runs SET prestart_input = 1
WHERE state IN ('claimed', 'cancel_requested') AND started_at IS NULL;
ALTER TABLE remote_deployment_claims ADD COLUMN expired_claims INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE remote_step_runs DROP COLUMN expired_claims;
ALTER TABLE remote_step_runs DROP COLUMN prestart_input;
ALTER TABLE remote_deployment_claims DROP COLUMN expired_claims;
