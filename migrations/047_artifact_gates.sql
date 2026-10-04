-- +goose Up
-- +goose StatementBegin
ALTER TABLE steps ADD COLUMN network_mode TEXT NOT NULL DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE steps ADD COLUMN approval_artifact_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE steps ADD COLUMN approval_review_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE steps ADD COLUMN approval_review_format TEXT NOT NULL DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
ALTER TABLE step_templates ADD COLUMN network_mode TEXT NOT NULL DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE step_templates ADD COLUMN approval_artifact_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE step_templates ADD COLUMN approval_review_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE step_templates ADD COLUMN approval_review_format TEXT NOT NULL DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
ALTER TABLE step_template_versions ADD COLUMN network_mode TEXT NOT NULL DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE step_template_versions ADD COLUMN approval_artifact_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE step_template_versions ADD COLUMN approval_review_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE step_template_versions ADD COLUMN approval_review_format TEXT NOT NULL DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
ALTER TABLE deployment_steps ADD COLUMN network_mode TEXT NOT NULL DEFAULT '' CHECK (network_mode IN ('', 'none', 'bridge'));
ALTER TABLE deployment_steps ADD COLUMN approval_artifact_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE deployment_steps ADD COLUMN approval_review_path TEXT NOT NULL DEFAULT '' ;
ALTER TABLE deployment_steps ADD COLUMN approval_review_format TEXT NOT NULL DEFAULT '' CHECK (approval_review_format IN ('', 'summary', 'terraform'));
CREATE TABLE artifact_gates (
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL CHECK (status IN ('awaiting', 'approved', 'rejected', 'expired', 'cancelled')),
    artifact_path TEXT NOT NULL,
    artifact_sha256 TEXT NOT NULL,
    bundle_sha256 TEXT NOT NULL,
    bundle_size INTEGER NOT NULL,
    review TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    approved_by INTEGER,
    approved_at INTEGER,
    PRIMARY KEY (deployment_id, step_index)
);
CREATE TABLE artifact_gate_chunks (
    deployment_id INTEGER NOT NULL,
    step_index INTEGER NOT NULL,
    chunk_index INTEGER NOT NULL,
    ciphertext TEXT NOT NULL,
    PRIMARY KEY (deployment_id, step_index, chunk_index),
    FOREIGN KEY (deployment_id, step_index) REFERENCES artifact_gates(deployment_id, step_index) ON DELETE CASCADE
);
CREATE TABLE artifact_gate_runs (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    next_step INTEGER NOT NULL DEFAULT 0,
    claimed INTEGER NOT NULL DEFAULT 0 CHECK (claimed IN (0, 1))
);
CREATE TABLE artifact_gate_images (
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    image_id TEXT NOT NULL,
    PRIMARY KEY (deployment_id, step_index)
);
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE TABLE artifact_gates_rollback_refused (guard INTEGER CHECK (guard = 0));
INSERT INTO artifact_gates_rollback_refused VALUES (1);
-- +goose StatementEnd
