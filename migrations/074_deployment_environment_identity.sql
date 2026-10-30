-- +goose Up
ALTER TABLE deployments ADD COLUMN environment_name_snapshot TEXT;
UPDATE deployments SET environment_name_snapshot = (
    SELECT name FROM environments WHERE environments.id = deployments.environment_id
) WHERE environment_name_snapshot IS NULL;

-- +goose Down
ALTER TABLE deployments DROP COLUMN environment_name_snapshot;
