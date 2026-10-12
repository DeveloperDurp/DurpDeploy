-- +goose Up
ALTER TABLE deployment_logs ADD is_step_metadata BIGINT NOT NULL
    CONSTRAINT df_deployment_log_metadata DEFAULT 0
    CONSTRAINT ck_deployment_log_metadata CHECK (is_step_metadata IN (0, 1));

UPDATE deployment_logs SET is_step_metadata = 1
WHERE step_state IS NOT NULL OR id IN (
    SELECT MIN(id) FROM deployment_logs
    GROUP BY deployment_id, step_index, step_name
);

CREATE INDEX idx_deployment_log_metadata
ON deployment_logs(deployment_id, id)
WHERE is_step_metadata = 1;

-- +goose Down
DROP INDEX idx_deployment_log_metadata ON deployment_logs;
ALTER TABLE deployment_logs DROP CONSTRAINT ck_deployment_log_metadata;
ALTER TABLE deployment_logs DROP CONSTRAINT df_deployment_log_metadata;
ALTER TABLE deployment_logs DROP COLUMN is_step_metadata;
