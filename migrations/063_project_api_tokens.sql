-- +goose Up
ALTER TABLE api_tokens ADD COLUMN token_role TEXT NOT NULL DEFAULT ''
    CHECK (token_role IN ('', 'viewer', 'deployer', 'admin'));
CREATE TABLE api_token_projects (
    token_id TEXT NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (token_id, project_id)
);
CREATE INDEX idx_api_token_projects_project ON api_token_projects(project_id);

-- +goose Down
-- Refuse rollback because old applications do not enforce project token grants.
CREATE TABLE project_api_tokens_rollback_refused (
    guard INTEGER CHECK (guard = 0)
);
INSERT INTO project_api_tokens_rollback_refused VALUES (1);
