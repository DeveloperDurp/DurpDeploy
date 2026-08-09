-- +goose Up

ALTER TABLE users ADD CONSTRAINT ck_users_role
    CHECK (role IN ('admin', 'deployer', 'viewer'));
ALTER TABLE project_members ADD CONSTRAINT ck_project_members_role
    CHECK (role IN ('admin', 'deployer'));
ALTER TABLE api_tokens ADD CONSTRAINT df_api_tokens_scope
    DEFAULT 'global' FOR scope;
ALTER TABLE api_tokens ADD CONSTRAINT ck_api_tokens_scope
    CHECK (scope IN ('global', 'scoped'));
