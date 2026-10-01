-- +goose Up
-- +goose StatementBegin
CREATE TABLE package_repositories (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id),
    name NVARCHAR(255) NOT NULL,
    url_template NVARCHAR(MAX) NOT NULL,
    auth_type NVARCHAR(16) NOT NULL CHECK (auth_type IN ('noauth', 'bearer', 'basic')),
    username NVARCHAR(255) NOT NULL DEFAULT '',
    credential NVARCHAR(MAX) NOT NULL DEFAULT '',
    UNIQUE(project_id, name)
);
CREATE TABLE project_artifact_repositories (
    project_id BIGINT PRIMARY KEY REFERENCES projects(id),
    repository_id BIGINT NOT NULL REFERENCES package_repositories(id)
);
CREATE TABLE release_artifacts (
    release_id BIGINT PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL REFERENCES package_repositories(id),
    url NVARCHAR(MAX) NOT NULL,
    version NVARCHAR(255) NOT NULL,
    sha256 NVARCHAR(64) NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0),
    source_release_id BIGINT REFERENCES releases(id)
);
CREATE TABLE deployment_artifacts (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL REFERENCES package_repositories(id),
    url NVARCHAR(MAX) NOT NULL,
    version NVARCHAR(255) NOT NULL,
    sha256 NVARCHAR(64) NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE artifacts_rollback_refused (
    guard BIGINT CONSTRAINT artifacts_require_forward_migration CHECK (guard = 0)
);
INSERT INTO artifacts_rollback_refused VALUES (1);
-- +goose StatementEnd
