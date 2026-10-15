-- +goose Up
ALTER TABLE api_tokens ADD token_role NVARCHAR(16) NOT NULL
    CONSTRAINT DF_api_tokens_token_role DEFAULT ''
    CONSTRAINT CK_api_tokens_token_role
    CHECK (token_role IN ('', 'viewer', 'deployer', 'admin'));
CREATE TABLE api_token_projects (
    token_id NVARCHAR(255) NOT NULL,
    project_id BIGINT NOT NULL,
    CONSTRAINT PK_api_token_projects PRIMARY KEY (token_id, project_id),
    CONSTRAINT FK_api_token_projects_token FOREIGN KEY (token_id)
        REFERENCES api_tokens(id) ON DELETE CASCADE,
    CONSTRAINT FK_api_token_projects_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX idx_api_token_projects_project ON api_token_projects(project_id);

-- +goose Down
-- Refuse rollback because old applications do not enforce project token grants.
CREATE TABLE project_api_tokens_rollback_refused (
    guard INT CHECK (guard = 0)
);
INSERT INTO project_api_tokens_rollback_refused VALUES (1);
