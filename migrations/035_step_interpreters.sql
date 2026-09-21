-- +goose Up
-- +goose StatementBegin
ALTER TABLE steps ADD COLUMN interpreter TEXT NOT NULL DEFAULT 'bash'
    CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
ALTER TABLE step_templates ADD COLUMN interpreter TEXT NOT NULL DEFAULT 'bash'
    CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
ALTER TABLE step_template_versions ADD COLUMN interpreter TEXT NOT NULL DEFAULT 'bash'
    CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
ALTER TABLE deployment_steps ADD COLUMN interpreter TEXT NOT NULL DEFAULT 'bash'
    CHECK (interpreter IN ('bash', 'pwsh', 'python3'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE step_interpreters_rollback_refused (
    guard INTEGER CONSTRAINT step_interpreters_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO step_interpreters_rollback_refused VALUES (1);
-- +goose StatementEnd
