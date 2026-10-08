-- +goose Up
-- +goose StatementBegin
CREATE TABLE environment_deployment_slots (
    environment_id INTEGER PRIMARY KEY REFERENCES environments(id),
    deployment_id INTEGER NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE
);
CREATE INDEX deployment_queue_order ON deployments(environment_id, status, created_at, id);
INSERT INTO environment_deployment_slots (environment_id, deployment_id)
SELECT environment_id, COALESCE(MIN(CASE WHEN status IN ('running', 'cleanup_unconfirmed') THEN id END), MIN(id)) FROM deployments
WHERE status IN ('pending', 'running', 'cleanup_unconfirmed')
GROUP BY environment_id;
UPDATE deployments SET status = 'queued'
WHERE status = 'pending' AND id NOT IN (SELECT deployment_id FROM environment_deployment_slots);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE deployment_queue_rollback_refused (
    guard INTEGER CONSTRAINT deployment_queue_requires_forward_migration CHECK (guard = 0)
);
INSERT INTO deployment_queue_rollback_refused VALUES (1);
-- +goose StatementEnd
