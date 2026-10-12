-- +goose Up
-- +goose StatementBegin
CREATE TABLE project_environment_deployment_slots (
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    project_id BIGINT NOT NULL REFERENCES projects(id),
    deployment_id BIGINT NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, environment_id)
);
INSERT INTO project_environment_deployment_slots (environment_id, project_id, deployment_id)
SELECT s.environment_id, r.project_id, s.deployment_id
FROM environment_deployment_slots s
JOIN deployments d ON d.id=s.deployment_id JOIN releases r ON r.id=d.release_id;
DROP TABLE environment_deployment_slots;
EXEC sp_rename 'project_environment_deployment_slots', 'environment_deployment_slots';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
THROW 51000, 'Project queue requires a forward migration', 1;
-- +goose StatementEnd
