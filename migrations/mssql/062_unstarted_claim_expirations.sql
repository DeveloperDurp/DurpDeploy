-- +goose Up
ALTER TABLE remote_step_runs ADD expired_claims BIGINT NOT NULL CONSTRAINT DF_remote_step_runs_expired_claims DEFAULT 0;
ALTER TABLE remote_step_runs ADD prestart_input BIGINT NOT NULL CONSTRAINT DF_remote_step_runs_prestart_input DEFAULT 0;
UPDATE remote_step_runs SET prestart_input = 1
WHERE state IN ('claimed', 'cancel_requested') AND started_at IS NULL;
ALTER TABLE remote_deployment_claims ADD expired_claims BIGINT NOT NULL CONSTRAINT DF_remote_deployment_claims_expired_claims DEFAULT 0;

-- +goose Down
ALTER TABLE remote_step_runs DROP CONSTRAINT DF_remote_step_runs_expired_claims;
ALTER TABLE remote_step_runs DROP COLUMN expired_claims;
ALTER TABLE remote_step_runs DROP CONSTRAINT DF_remote_step_runs_prestart_input;
ALTER TABLE remote_step_runs DROP COLUMN prestart_input;
ALTER TABLE remote_deployment_claims DROP CONSTRAINT DF_remote_deployment_claims_expired_claims;
ALTER TABLE remote_deployment_claims DROP COLUMN expired_claims;
