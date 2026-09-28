-- +goose Up
-- +goose StatementBegin
ALTER TABLE steps ADD container_image NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_steps_container_image DEFAULT '';
ALTER TABLE steps ADD variable_names NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_steps_variable_names DEFAULT '[]';
ALTER TABLE step_templates ADD container_image NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_step_templates_container_image DEFAULT '';
ALTER TABLE step_templates ADD variable_names NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_step_templates_variable_names DEFAULT '[]';
ALTER TABLE step_template_versions ADD container_image NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_step_template_versions_container_image DEFAULT '';
ALTER TABLE step_template_versions ADD variable_names NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_step_template_versions_variable_names DEFAULT '[]';
ALTER TABLE deployment_steps ADD container_image NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_deployment_steps_container_image DEFAULT '';
ALTER TABLE deployment_steps ADD variable_names NVARCHAR(MAX) NOT NULL
    CONSTRAINT DF_deployment_steps_variable_names DEFAULT '[]';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE step_container_metadata_rollback_refused (
    guard BIGINT CONSTRAINT step_container_metadata_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO step_container_metadata_rollback_refused VALUES (1);
-- +goose StatementEnd
