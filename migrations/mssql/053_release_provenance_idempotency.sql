-- +goose Up
-- Version 052 is reserved by the open optimization roadmap MR !66.
ALTER TABLE releases ADD repository_url NVARCHAR(2048) NOT NULL CONSTRAINT DF_releases_repository_url DEFAULT '';
ALTER TABLE releases ADD commit_sha NVARCHAR(64) NOT NULL CONSTRAINT DF_releases_commit_sha DEFAULT '';
ALTER TABLE releases ADD artifact_ref NVARCHAR(2048) NOT NULL CONSTRAINT DF_releases_artifact_ref DEFAULT '';
ALTER TABLE releases ADD ci_run_url NVARCHAR(2048) NOT NULL CONSTRAINT DF_releases_ci_run_url DEFAULT '';

CREATE TABLE api_idempotency (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    operation VARCHAR(20) NOT NULL,
    key_hash CHAR(64) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    response_json NVARCHAR(MAX) NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, project_id, operation, key_hash)
);

-- +goose Down
DROP TABLE api_idempotency;
ALTER TABLE releases DROP CONSTRAINT DF_releases_ci_run_url;
ALTER TABLE releases DROP CONSTRAINT DF_releases_artifact_ref;
ALTER TABLE releases DROP CONSTRAINT DF_releases_commit_sha;
ALTER TABLE releases DROP CONSTRAINT DF_releases_repository_url;
ALTER TABLE releases DROP COLUMN ci_run_url, artifact_ref, commit_sha, repository_url;
