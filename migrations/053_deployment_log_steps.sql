-- +goose Up
ALTER TABLE deployment_logs ADD COLUMN step_index INTEGER CHECK (step_index >= 0);
ALTER TABLE deployment_logs ADD COLUMN step_state TEXT CHECK (
    step_state IN ('waiting', 'running', 'succeeded', 'failed', 'cancelled')
);

-- +goose Down
ALTER TABLE deployment_logs DROP COLUMN step_state;
ALTER TABLE deployment_logs DROP COLUMN step_index;
