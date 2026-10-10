-- +goose Up
-- +goose StatementBegin
CREATE TABLE release_artifacts_new (
    release_id INTEGER PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL REFERENCES package_repositories(id),
    url TEXT NOT NULL,
    version TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER NOT NULL,
    source_release_id INTEGER REFERENCES releases(id),
    resolution_key TEXT NOT NULL DEFAULT '',
    CHECK ((sha256 = '' AND size = 0 AND resolution_key <> '') OR (sha256 <> '' AND size > 0))
);
INSERT INTO release_artifacts_new (release_id, repository_id, url, version, sha256, size, source_release_id)
SELECT release_id, repository_id, url, version, sha256, size, source_release_id FROM release_artifacts;
DROP TABLE release_artifacts;
ALTER TABLE release_artifacts_new RENAME TO release_artifacts;
CREATE INDEX release_artifact_resolution ON release_artifacts(resolution_key);

CREATE TABLE deployment_artifacts_new (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL REFERENCES package_repositories(id),
    url TEXT NOT NULL,
    version TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER NOT NULL,
    resolution_key TEXT NOT NULL DEFAULT '',
    CHECK ((sha256 = '' AND size = 0 AND resolution_key <> '') OR (sha256 <> '' AND size > 0))
);
INSERT INTO deployment_artifacts_new (deployment_id, repository_id, url, version, sha256, size)
SELECT deployment_id, repository_id, url, version, sha256, size FROM deployment_artifacts;
DROP TABLE deployment_artifacts;
ALTER TABLE deployment_artifacts_new RENAME TO deployment_artifacts;
CREATE INDEX deployment_artifact_resolution ON deployment_artifacts(resolution_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE deferred_artifacts_rollback_refused (
    guard INTEGER CHECK (guard = 0)
);
INSERT INTO deferred_artifacts_rollback_refused VALUES (1);
-- +goose StatementEnd
