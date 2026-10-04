-- +goose Up
-- +goose StatementBegin
ALTER TABLE steps ADD network_mode NVARCHAR(1024) NOT NULL CONSTRAINT df_steps_network_mode DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE steps ADD approval_artifact_path NVARCHAR(1024) NOT NULL CONSTRAINT df_steps_approval_artifact_path DEFAULT '' ;
ALTER TABLE steps ADD approval_review_path NVARCHAR(1024) NOT NULL CONSTRAINT df_steps_approval_review_path DEFAULT '' ;
ALTER TABLE steps ADD approval_review_format NVARCHAR(1024) NOT NULL CONSTRAINT df_steps_approval_review_format DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
ALTER TABLE step_templates ADD network_mode NVARCHAR(1024) NOT NULL CONSTRAINT df_step_templates_network_mode DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE step_templates ADD approval_artifact_path NVARCHAR(1024) NOT NULL CONSTRAINT df_step_templates_approval_artifact_path DEFAULT '' ;
ALTER TABLE step_templates ADD approval_review_path NVARCHAR(1024) NOT NULL CONSTRAINT df_step_templates_approval_review_path DEFAULT '' ;
ALTER TABLE step_templates ADD approval_review_format NVARCHAR(1024) NOT NULL CONSTRAINT df_step_templates_approval_review_format DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
ALTER TABLE step_template_versions ADD network_mode NVARCHAR(1024) NOT NULL CONSTRAINT df_step_template_versions_network_mode DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE step_template_versions ADD approval_artifact_path NVARCHAR(1024) NOT NULL CONSTRAINT df_step_template_versions_approval_artifact_path DEFAULT '' ;
ALTER TABLE step_template_versions ADD approval_review_path NVARCHAR(1024) NOT NULL CONSTRAINT df_step_template_versions_approval_review_path DEFAULT '' ;
ALTER TABLE step_template_versions ADD approval_review_format NVARCHAR(1024) NOT NULL CONSTRAINT df_step_template_versions_approval_review_format DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
ALTER TABLE deployment_steps ADD network_mode NVARCHAR(1024) NOT NULL CONSTRAINT df_deployment_steps_network_mode DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE deployment_steps ADD approval_artifact_path NVARCHAR(1024) NOT NULL CONSTRAINT df_deployment_steps_approval_artifact_path DEFAULT '' ;
ALTER TABLE deployment_steps ADD approval_review_path NVARCHAR(1024) NOT NULL CONSTRAINT df_deployment_steps_approval_review_path DEFAULT '' ;
ALTER TABLE deployment_steps ADD approval_review_format NVARCHAR(1024) NOT NULL CONSTRAINT df_deployment_steps_approval_review_format DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
CREATE TABLE artifact_gates (
    deployment_id BIGINT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_index BIGINT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    status NVARCHAR(128) NOT NULL CHECK (status IN ('awaiting', 'approved', 'rejected', 'expired', 'cancelled')),
    artifact_path NVARCHAR(128) NOT NULL,
    artifact_sha256 NVARCHAR(128) NOT NULL,
    bundle_sha256 NVARCHAR(128) NOT NULL,
    bundle_size BIGINT NOT NULL,
    review NVARCHAR(MAX) NOT NULL,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    approved_by BIGINT,
    approved_at BIGINT,
    PRIMARY KEY (deployment_id, step_index)
);
CREATE TABLE artifact_gate_chunks (
    deployment_id BIGINT NOT NULL,
    step_index BIGINT NOT NULL,
    chunk_index BIGINT NOT NULL,
    ciphertext NVARCHAR(MAX) NOT NULL,
    PRIMARY KEY (deployment_id, step_index, chunk_index),
    FOREIGN KEY (deployment_id, step_index) REFERENCES artifact_gates(deployment_id, step_index) ON DELETE CASCADE
);
CREATE TABLE artifact_gate_runs (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    next_step BIGINT NOT NULL DEFAULT 0,
    claimed BIGINT NOT NULL DEFAULT 0 CHECK (claimed IN (0, 1))
);
CREATE TABLE artifact_gate_images (
    deployment_id BIGINT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_index BIGINT NOT NULL,
    image_id NVARCHAR(128) NOT NULL,
    PRIMARY KEY (deployment_id, step_index)
);
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
THROW 51000, 'Artifact gates require a forward migration.', 1;
-- +goose StatementEnd
