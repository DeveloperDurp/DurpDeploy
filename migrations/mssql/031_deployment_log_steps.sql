-- +goose Up
ALTER TABLE deployment_logs ADD step_index BIGINT NULL
    CONSTRAINT ck_deployment_log_step_index CHECK (step_index >= 0);
ALTER TABLE deployment_logs ADD step_state NVARCHAR(32) NULL
    CONSTRAINT ck_deployment_log_step_state CHECK (
        step_state IN ('waiting', 'running', 'succeeded', 'failed', 'cancelled')
    );

-- +goose Down
ALTER TABLE deployment_logs DROP CONSTRAINT ck_deployment_log_step_state;
ALTER TABLE deployment_logs DROP COLUMN step_state;
ALTER TABLE deployment_logs DROP CONSTRAINT ck_deployment_log_step_index;
ALTER TABLE deployment_logs DROP COLUMN step_index;
