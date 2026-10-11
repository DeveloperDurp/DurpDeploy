-- +goose Up
CREATE TABLE agent_staging_capabilities (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE
);
CREATE TABLE deployment_staging_snapshots (
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    producer TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size > 0 AND size <= 570425344),
    PRIMARY KEY (deployment_id, step_index, producer)
);
CREATE TABLE deployment_staging_chunks (
    deployment_id INTEGER NOT NULL,
    step_index INTEGER NOT NULL,
    producer TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    ciphertext TEXT NOT NULL,
    PRIMARY KEY (deployment_id, step_index, producer, chunk_index),
    FOREIGN KEY (deployment_id, step_index, producer)
      REFERENCES deployment_staging_snapshots(deployment_id, step_index, producer) ON DELETE CASCADE
);
DROP VIEW eligible_remote_step_agents;
CREATE VIEW eligible_remote_step_agents AS
SELECT s.deployment_id, s.step_index, a.id AS agent_id
FROM deployment_steps s JOIN deployments d ON d.id = s.deployment_id
JOIN agents a ON a.status = 'active' AND a.revoked_at IS NULL
JOIN agent_pairings p ON p.agent_id = a.id AND p.state = 'paired'
JOIN agent_environment_labels e ON e.agent_id = a.id AND e.environment_id = d.environment_id
JOIN agent_step_capabilities c ON c.agent_id = a.id
    AND c.interpreter = s.interpreter AND c.execution_mode = s.agent_execution_mode
WHERE s.execution_target = 'agent' AND NOT EXISTS (
    SELECT 1 FROM deployment_step_selectors wanted
    WHERE wanted.deployment_id = s.deployment_id AND wanted.step_index = s.step_index
      AND NOT EXISTS (SELECT 1 FROM agent_labels owned
          WHERE owned.agent_id = a.id AND lower(owned.label) = lower(wanted.label))
) AND (NOT EXISTS (SELECT 1 FROM deployment_staging_snapshots stage
    WHERE stage.deployment_id = s.deployment_id AND stage.step_index = s.step_index AND stage.producer = '')
    OR EXISTS (SELECT 1 FROM agent_staging_capabilities stage WHERE stage.agent_id = a.id));

-- +goose Down
CREATE TABLE staging_rollback_refused (guard INTEGER CHECK (guard = 0));
INSERT INTO staging_rollback_refused VALUES (1);
