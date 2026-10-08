-- +goose Up
-- +goose StatementBegin
CREATE TABLE environment_deployment_slots (
    environment_id BIGINT PRIMARY KEY REFERENCES environments(id),
    deployment_id BIGINT NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE
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
THROW 51000, 'Deployment queue requires a forward migration', 1;
-- +goose StatementEnd
