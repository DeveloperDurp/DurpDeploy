-- +goose Up
-- +goose StatementBegin
ALTER TABLE steps ADD interpreter NVARCHAR(16) NOT NULL
    CONSTRAINT DF_steps_interpreter DEFAULT 'bash',
    CONSTRAINT CK_steps_interpreter CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
ALTER TABLE step_templates ADD interpreter NVARCHAR(16) NOT NULL
    CONSTRAINT DF_step_templates_interpreter DEFAULT 'bash',
    CONSTRAINT CK_step_templates_interpreter CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
ALTER TABLE step_template_versions ADD interpreter NVARCHAR(16) NOT NULL
    CONSTRAINT DF_step_template_versions_interpreter DEFAULT 'bash',
    CONSTRAINT CK_step_template_versions_interpreter CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
ALTER TABLE deployment_steps ADD interpreter NVARCHAR(16) NOT NULL
    CONSTRAINT DF_deployment_steps_interpreter DEFAULT 'bash',
    CONSTRAINT CK_deployment_steps_interpreter CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE step_interpreters_rollback_refused (
    guard BIGINT CONSTRAINT step_interpreters_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO step_interpreters_rollback_refused VALUES (1);
-- +goose StatementEnd
