-- +goose Up
INSERT INTO agent_environment_labels (agent_id, environment_id)
SELECT assignment.agent_id, assignment.environment_id
FROM environment_agent_assignments assignment
WHERE NOT EXISTS (
    SELECT 1 FROM agent_environment_labels label
    WHERE label.agent_id = assignment.agent_id
      AND label.environment_id = assignment.environment_id
);
DROP TABLE environment_agent_assignments;

-- +goose Down
CREATE TABLE environment_agent_assignments_rollback_refused (
    guard BIGINT CONSTRAINT environment_agent_assignments_require_forward_migration
        CHECK (guard = 0)
);
INSERT INTO environment_agent_assignments_rollback_refused VALUES (1);
