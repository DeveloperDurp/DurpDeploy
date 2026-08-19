-- +goose Up

DROP INDEX idx_deployment_logs_deployment_sequence ON deployment_logs;
ALTER TABLE deployment_logs ALTER COLUMN sequence BIGINT NOT NULL;
CREATE UNIQUE INDEX idx_deployment_logs_deployment_sequence
    ON deployment_logs(deployment_id, sequence);

-- +goose Down

DROP INDEX idx_deployment_logs_deployment_sequence ON deployment_logs;
ALTER TABLE deployment_logs ALTER COLUMN sequence BIGINT NULL;
CREATE UNIQUE INDEX idx_deployment_logs_deployment_sequence
    ON deployment_logs(deployment_id, sequence)
    WHERE sequence IS NOT NULL;
