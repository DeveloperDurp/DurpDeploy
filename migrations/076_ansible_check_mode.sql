-- +goose Up
ALTER TABLE deployments ADD COLUMN ansible_check_mode INTEGER CHECK (ansible_check_mode IN (0, 1));

-- +goose Down
ALTER TABLE deployments DROP COLUMN ansible_check_mode;
