-- +goose Up
ALTER TABLE steps ADD kind NVARCHAR(16) NOT NULL CONSTRAINT DF_steps_kind DEFAULT 'script';
ALTER TABLE steps ADD ansible_json NVARCHAR(MAX) NOT NULL CONSTRAINT DF_steps_ansible DEFAULT '';
ALTER TABLE step_templates ADD kind NVARCHAR(16) NOT NULL CONSTRAINT DF_step_templates_kind DEFAULT 'script';
ALTER TABLE step_templates ADD ansible_json NVARCHAR(MAX) NOT NULL CONSTRAINT DF_step_templates_ansible DEFAULT '';
ALTER TABLE step_template_versions ADD kind NVARCHAR(16) NOT NULL CONSTRAINT DF_step_template_versions_kind DEFAULT 'script';
ALTER TABLE step_template_versions ADD ansible_json NVARCHAR(MAX) NOT NULL CONSTRAINT DF_step_template_versions_ansible DEFAULT '';
ALTER TABLE deployment_steps ADD kind NVARCHAR(16) NOT NULL CONSTRAINT DF_deployment_steps_kind DEFAULT 'script';
ALTER TABLE deployment_steps ADD ansible_json NVARCHAR(MAX) NOT NULL CONSTRAINT DF_deployment_steps_ansible DEFAULT '';

-- +goose Down
ALTER TABLE deployment_steps DROP CONSTRAINT DF_deployment_steps_ansible;
ALTER TABLE deployment_steps DROP CONSTRAINT DF_deployment_steps_kind;
ALTER TABLE deployment_steps DROP COLUMN ansible_json;
ALTER TABLE deployment_steps DROP COLUMN kind;
ALTER TABLE step_template_versions DROP CONSTRAINT DF_step_template_versions_ansible;
ALTER TABLE step_template_versions DROP CONSTRAINT DF_step_template_versions_kind;
ALTER TABLE step_template_versions DROP COLUMN ansible_json;
ALTER TABLE step_template_versions DROP COLUMN kind;
ALTER TABLE step_templates DROP CONSTRAINT DF_step_templates_ansible;
ALTER TABLE step_templates DROP CONSTRAINT DF_step_templates_kind;
ALTER TABLE step_templates DROP COLUMN ansible_json;
ALTER TABLE step_templates DROP COLUMN kind;
ALTER TABLE steps DROP CONSTRAINT DF_steps_ansible;
ALTER TABLE steps DROP CONSTRAINT DF_steps_kind;
ALTER TABLE steps DROP COLUMN ansible_json;
ALTER TABLE steps DROP COLUMN kind;
