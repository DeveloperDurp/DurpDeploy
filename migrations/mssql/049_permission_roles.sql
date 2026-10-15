-- +goose Up
ALTER TABLE users DROP CONSTRAINT ck_users_role;
ALTER TABLE users ADD CONSTRAINT ck_users_role
    CHECK (role IN ('viewer', 'deployer', 'maintainer', 'admin'));
ALTER TABLE project_members DROP CONSTRAINT ck_project_members_role;
ALTER TABLE project_members ADD CONSTRAINT ck_project_members_role
    CHECK (role IN ('viewer', 'deployer', 'maintainer', 'admin'));
ALTER TABLE api_tokens DROP CONSTRAINT CK_api_tokens_token_role;
ALTER TABLE api_tokens ADD CONSTRAINT CK_api_tokens_token_role
    CHECK (token_role IN ('', 'viewer', 'deployer', 'maintainer', 'admin'));
UPDATE users SET role = 'maintainer' WHERE role = 'deployer';
UPDATE project_members SET role = 'maintainer' WHERE role = 'deployer';
UPDATE api_tokens SET token_role = 'maintainer' WHERE token_role = 'deployer';

-- +goose Down
CREATE TABLE permission_roles_rollback_refused (guard INT CHECK (guard = 0));
INSERT INTO permission_roles_rollback_refused VALUES (1);
