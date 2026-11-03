-- +goose Up
ALTER TABLE deployments ADD ansible_check_mode BIGINT NULL CONSTRAINT CK_deployments_ansible_check_mode CHECK (ansible_check_mode IN (0, 1));

-- +goose Down
ALTER TABLE deployments DROP CONSTRAINT CK_deployments_ansible_check_mode;
ALTER TABLE deployments DROP COLUMN ansible_check_mode;
