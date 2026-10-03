-- +goose Up
-- +goose StatementBegin
ALTER TABLE environments ADD verification_type NVARCHAR(16) NOT NULL CONSTRAINT df_environment_verification_type DEFAULT '' CHECK (verification_type IN ('', 'http', 'bash'));
ALTER TABLE environments ADD verification_target NVARCHAR(MAX) NOT NULL CONSTRAINT df_environment_verification_target DEFAULT '';
ALTER TABLE environments ADD verification_timeout_seconds BIGINT NOT NULL CONSTRAINT df_environment_verification_timeout DEFAULT 30 CHECK (verification_timeout_seconds BETWEEN 1 AND 300);
ALTER TABLE releases ADD snapshot_locked BIGINT NOT NULL CONSTRAINT df_release_snapshot_locked DEFAULT 0 CHECK (snapshot_locked IN (0, 1));
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE releases SET snapshot_locked = 1 WHERE EXISTS (SELECT 1 FROM deployments WHERE deployments.release_id = releases.id);
CREATE TABLE deployment_verifications (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    type NVARCHAR(16) NOT NULL CHECK (type IN ('http', 'bash')),
    target NVARCHAR(MAX) NOT NULL,
    timeout_seconds BIGINT NOT NULL CHECK (timeout_seconds BETWEEN 1 AND 300),
    step_index BIGINT NOT NULL,
    status NVARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    started_at BIGINT,
    finished_at BIGINT
);
CREATE TABLE deployment_rollbacks (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    source_deployment_id BIGINT NOT NULL,
    target_deployment_id BIGINT NOT NULL,
    source_version NVARCHAR(255) NOT NULL,
    target_version NVARCHAR(255) NOT NULL
);
CREATE INDEX deployment_rollback_lookup ON deployments(environment_id, kind, created_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
THROW 51000, 'Verification and rollback require a forward migration.', 1;
-- +goose StatementEnd
