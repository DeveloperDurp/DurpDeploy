-- +goose Up
-- +goose StatementBegin
CREATE TABLE package_repositories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    url_template TEXT NOT NULL,
    auth_type TEXT NOT NULL CHECK (auth_type IN ('noauth', 'bearer', 'basic')),
    username TEXT NOT NULL DEFAULT '',
    credential TEXT NOT NULL DEFAULT '',
    UNIQUE(project_id, name)
);
CREATE TABLE project_artifact_repositories (
    project_id INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL REFERENCES package_repositories(id)
);
CREATE TABLE release_artifacts (
    release_id INTEGER PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL REFERENCES package_repositories(id),
    url TEXT NOT NULL,
    version TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size > 0),
    source_release_id INTEGER REFERENCES releases(id)
);
CREATE TABLE deployment_artifacts (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL REFERENCES package_repositories(id),
    url TEXT NOT NULL,
    version TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size > 0)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE deployment_artifacts;
DROP TABLE release_artifacts;
DROP TABLE project_artifact_repositories;
DROP TABLE package_repositories;
-- +goose StatementEnd
