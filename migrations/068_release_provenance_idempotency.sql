-- +goose Up
-- Version 067 is reserved by the open optimization roadmap MR !66.
ALTER TABLE releases ADD COLUMN repository_url TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN artifact_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN ci_run_url TEXT NOT NULL DEFAULT '';

CREATE TABLE api_idempotency (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    operation TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, project_id, operation, key_hash)
);

-- +goose Down
DROP TABLE api_idempotency;
ALTER TABLE releases DROP COLUMN ci_run_url;
ALTER TABLE releases DROP COLUMN artifact_ref;
ALTER TABLE releases DROP COLUMN commit_sha;
ALTER TABLE releases DROP COLUMN repository_url;
