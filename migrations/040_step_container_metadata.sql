-- +goose Up
-- +goose StatementBegin
ALTER TABLE steps ADD COLUMN container_image TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN variable_names TEXT NOT NULL DEFAULT '[]';
ALTER TABLE step_templates ADD COLUMN container_image TEXT NOT NULL DEFAULT '';
ALTER TABLE step_templates ADD COLUMN variable_names TEXT NOT NULL DEFAULT '[]';
ALTER TABLE step_template_versions ADD COLUMN container_image TEXT NOT NULL DEFAULT '';
ALTER TABLE step_template_versions ADD COLUMN variable_names TEXT NOT NULL DEFAULT '[]';
ALTER TABLE deployment_steps ADD COLUMN container_image TEXT NOT NULL DEFAULT '';
ALTER TABLE deployment_steps ADD COLUMN variable_names TEXT NOT NULL DEFAULT '[]';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE step_container_metadata_rollback_refused (
    guard INTEGER CONSTRAINT step_container_metadata_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO step_container_metadata_rollback_refused VALUES (1);

-- +goose StatementEnd
