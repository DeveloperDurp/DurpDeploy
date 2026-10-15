-- +goose Up
-- Replace only role columns. Native DROP COLUMN preserves row identities,
-- foreign keys, indexes, credentials, and all unrelated columns.
CREATE TABLE permission_roles_users AS SELECT id, role FROM users;
ALTER TABLE users DROP COLUMN role;
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'viewer'
    CHECK (role IN ('viewer', 'deployer', 'maintainer', 'admin'));
UPDATE users SET role = (SELECT CASE WHEN old.role = 'deployer'
    THEN 'maintainer' ELSE old.role END
    FROM permission_roles_users old WHERE old.id = users.id);
DROP TABLE permission_roles_users;

CREATE TABLE permission_roles_members AS
    SELECT project_id, user_id, role FROM project_members;
ALTER TABLE project_members DROP COLUMN role;
ALTER TABLE project_members ADD COLUMN role TEXT NOT NULL DEFAULT 'viewer'
    CHECK (role IN ('viewer', 'deployer', 'maintainer', 'admin'));
UPDATE project_members SET role = (SELECT CASE WHEN old.role = 'deployer'
    THEN 'maintainer' ELSE old.role END FROM permission_roles_members old
    WHERE old.project_id = project_members.project_id
    AND old.user_id = project_members.user_id);
DROP TABLE permission_roles_members;

CREATE TABLE permission_roles_tokens AS SELECT id, token_role FROM api_tokens;
ALTER TABLE api_tokens DROP COLUMN token_role;
ALTER TABLE api_tokens ADD COLUMN token_role TEXT NOT NULL DEFAULT ''
    CHECK (token_role IN ('', 'viewer', 'deployer', 'maintainer', 'admin'));
UPDATE api_tokens SET token_role = (SELECT CASE WHEN old.token_role = 'deployer'
    THEN 'maintainer' ELSE old.token_role END
    FROM permission_roles_tokens old WHERE old.id = api_tokens.id);
DROP TABLE permission_roles_tokens;

-- +goose Down
-- Older applications grant authoring permission to restricted Deployers.
CREATE TABLE permission_roles_rollback_refused (guard INTEGER CHECK (guard = 0));
INSERT INTO permission_roles_rollback_refused VALUES (1);
