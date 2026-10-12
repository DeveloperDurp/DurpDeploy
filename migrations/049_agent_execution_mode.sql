-- +goose Up
ALTER TABLE steps ADD COLUMN agent_execution_mode TEXT NOT NULL DEFAULT 'host'
    CHECK (agent_execution_mode IN ('host', 'container'));
ALTER TABLE step_templates ADD COLUMN agent_execution_mode TEXT NOT NULL DEFAULT 'host'
    CHECK (agent_execution_mode IN ('host', 'container'));
ALTER TABLE step_template_versions ADD COLUMN agent_execution_mode TEXT NOT NULL DEFAULT 'host'
    CHECK (agent_execution_mode IN ('host', 'container'));
ALTER TABLE deployment_steps ADD COLUMN agent_execution_mode TEXT NOT NULL DEFAULT 'host'
    CHECK (agent_execution_mode IN ('host', 'container'));

CREATE VIEW agent_step_capabilities AS
SELECT i.agent_id, 'host' AS execution_mode, i.interpreter
FROM agent_interpreters i JOIN agents a ON a.id = i.agent_id
WHERE (COALESCE(a.agent_protocol, 'agent/1') != 'agent/1' OR i.interpreter = 'bash')
  AND (COALESCE(a.agent_protocol, 'agent/1') != 'agent/3' OR EXISTS (
      SELECT 1 FROM agent_execution_modes m
      WHERE m.agent_id = i.agent_id AND m.execution_mode = 'host'))
UNION ALL
SELECT i.agent_id, 'container' AS execution_mode, i.interpreter
FROM agent_container_interpreters i JOIN agents a ON a.id = i.agent_id
WHERE a.agent_protocol = 'agent/3'
  AND EXISTS (SELECT 1 FROM agent_execution_modes m
      WHERE m.agent_id = i.agent_id AND m.execution_mode = 'container')
  AND EXISTS (SELECT 1 FROM agent_container_runtimes r WHERE r.agent_id = i.agent_id);

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
);

-- +goose Down
CREATE TABLE agent_execution_mode_rollback_refused (guard INTEGER CHECK (guard = 0));
INSERT INTO agent_execution_mode_rollback_refused VALUES (1);
