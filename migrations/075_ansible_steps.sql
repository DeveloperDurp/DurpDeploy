-- +goose Up
ALTER TABLE steps ADD COLUMN kind TEXT NOT NULL DEFAULT 'script' CHECK (kind IN ('script', 'ansible'));
ALTER TABLE steps ADD COLUMN ansible_json TEXT NOT NULL DEFAULT '';
ALTER TABLE step_templates ADD COLUMN kind TEXT NOT NULL DEFAULT 'script' CHECK (kind IN ('script', 'ansible'));
ALTER TABLE step_templates ADD COLUMN ansible_json TEXT NOT NULL DEFAULT '';
ALTER TABLE step_template_versions ADD COLUMN kind TEXT NOT NULL DEFAULT 'script' CHECK (kind IN ('script', 'ansible'));
ALTER TABLE step_template_versions ADD COLUMN ansible_json TEXT NOT NULL DEFAULT '';
ALTER TABLE deployment_steps ADD COLUMN kind TEXT NOT NULL DEFAULT 'script' CHECK (kind IN ('script', 'ansible'));
ALTER TABLE deployment_steps ADD COLUMN ansible_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployment_steps DROP COLUMN ansible_json;
ALTER TABLE deployment_steps DROP COLUMN kind;
ALTER TABLE step_template_versions DROP COLUMN ansible_json;
ALTER TABLE step_template_versions DROP COLUMN kind;
ALTER TABLE step_templates DROP COLUMN ansible_json;
ALTER TABLE step_templates DROP COLUMN kind;
ALTER TABLE steps DROP COLUMN ansible_json;
ALTER TABLE steps DROP COLUMN kind;
