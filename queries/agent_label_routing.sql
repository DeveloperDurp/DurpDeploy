-- name: CreateAgentLabel :one
INSERT INTO agent_labels (name, normalized_name)
VALUES (sqlc.arg(name), sqlc.arg(normalized_name))
RETURNING *;

-- name: GetAgentLabel :one
SELECT * FROM agent_labels WHERE id = ?;

-- name: ListAgentLabels :many
SELECT * FROM agent_labels ORDER BY normalized_name ASC, id ASC;

-- name: UpdateAgentLabel :one
UPDATE agent_labels
SET name = sqlc.arg(name),
    normalized_name = sqlc.arg(normalized_name),
    updated_at = unixepoch()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteAgentLabel :execrows
DELETE FROM agent_labels WHERE id = ?;

-- name: CreateAgentLabelMembership :one
INSERT INTO agent_label_memberships (agent_label_id, agent_id)
SELECT sqlc.arg(agent_label_id), sqlc.arg(agent_id)
WHERE EXISTS (
    SELECT 1
    FROM agents
    JOIN agent_pairings ON agent_pairings.agent_id = agents.id
    WHERE agents.id = sqlc.arg(agent_id)
      AND agents.status = 'active'
      AND agent_pairings.state = 'paired'
)
RETURNING *;

-- name: DeleteAgentLabelMembership :execrows
DELETE FROM agent_label_memberships
WHERE agent_label_id = sqlc.arg(agent_label_id)
  AND agent_id = sqlc.arg(agent_id);

-- name: ListAgentLabelMemberships :many
SELECT membership.*, agent.name AS agent_name, agent.status AS agent_status,
       pairing.state AS pairing_state
FROM agent_label_memberships AS membership
JOIN agents AS agent ON agent.id = membership.agent_id
LEFT JOIN agent_pairings AS pairing ON pairing.agent_id = agent.id
WHERE membership.agent_label_id = ?
ORDER BY agent.name ASC, agent.id ASC;

-- name: ListEligibleAgentLabelMembers :many
SELECT agent.id, agent.name
FROM agent_label_memberships AS membership
JOIN agents AS agent ON agent.id = membership.agent_id
JOIN agent_pairings AS pairing ON pairing.agent_id = agent.id
WHERE membership.agent_label_id = ?
  AND agent.status = 'active'
  AND pairing.state = 'paired'
ORDER BY agent.id ASC;

-- name: CreateProjectExecutionPolicy :one
INSERT INTO project_execution_policies (
    project_id, target_mode, agent_label_id, agent_strategy
) VALUES (
    sqlc.arg(project_id), sqlc.arg(target_mode),
    sqlc.narg(agent_label_id), sqlc.narg(agent_strategy)
)
RETURNING *;

-- name: GetProjectExecutionPolicy :one
SELECT * FROM project_execution_policies WHERE project_id = ?;

-- name: UpdateProjectExecutionPolicy :one
UPDATE project_execution_policies
SET target_mode = sqlc.arg(target_mode),
    agent_label_id = sqlc.narg(agent_label_id),
    agent_strategy = sqlc.narg(agent_strategy),
    updated_at = unixepoch()
WHERE project_id = sqlc.arg(project_id)
RETURNING *;

-- name: DeleteProjectExecutionPolicy :execrows
DELETE FROM project_execution_policies WHERE project_id = ?;

-- name: CreateScheduledDeploymentRoutingPolicy :one
INSERT INTO scheduled_deployment_routing_policies (
    scheduled_deployment_id, target_mode, agent_label_id, agent_strategy
) VALUES (
    sqlc.arg(scheduled_deployment_id), sqlc.arg(target_mode),
    sqlc.narg(agent_label_id), sqlc.narg(agent_strategy)
)
RETURNING *;

-- name: GetScheduledDeploymentRoutingPolicy :one
SELECT * FROM scheduled_deployment_routing_policies
WHERE scheduled_deployment_id = ?;

-- name: UpdateScheduledDeploymentRoutingPolicy :one
UPDATE scheduled_deployment_routing_policies
SET target_mode = sqlc.arg(target_mode),
    agent_label_id = sqlc.narg(agent_label_id),
    agent_strategy = sqlc.narg(agent_strategy),
    updated_at = unixepoch()
WHERE scheduled_deployment_id = sqlc.arg(scheduled_deployment_id)
RETURNING *;

-- name: DeleteScheduledDeploymentRoutingPolicy :execrows
DELETE FROM scheduled_deployment_routing_policies
WHERE scheduled_deployment_id = ?;

-- name: CreateDeploymentRoutingSnapshot :one
INSERT INTO deployment_routing_snapshots (
    deployment_id, source, target_mode, agent_label_id,
    agent_label_name, agent_strategy
) VALUES (
    sqlc.arg(deployment_id), sqlc.arg(source), sqlc.arg(target_mode),
    sqlc.narg(agent_label_id), sqlc.narg(agent_label_name),
    sqlc.narg(agent_strategy)
)
RETURNING *;

-- name: GetDeploymentRoutingSnapshot :one
SELECT * FROM deployment_routing_snapshots WHERE deployment_id = ?;

-- name: AddDeploymentRoutingAgent :one
INSERT INTO deployment_routing_agents (
    deployment_id, position, agent_id, agent_name
) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: ListDeploymentRoutingAgents :many
SELECT * FROM deployment_routing_agents
WHERE deployment_id = ?
ORDER BY position ASC;

-- name: CreateRoutingChildDeployment :one
INSERT INTO deployments (
    release_id, environment_id, status, forced, note,
    parent_deployment_id, target_agent_id, target_agent_name
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListDeploymentChildren :many
SELECT * FROM deployments
WHERE parent_deployment_id = ?
ORDER BY id ASC;

-- name: CreateAgentLabelCursor :one
INSERT INTO agent_label_cursors (agent_label_id, last_agent_id)
VALUES (?, ?)
RETURNING *;

-- name: EnsureAgentLabelCursor :exec
INSERT INTO agent_label_cursors (agent_label_id, last_agent_id)
VALUES (?, NULL)
ON CONFLICT(agent_label_id) DO NOTHING;

-- name: GetAgentLabelCursor :one
SELECT * FROM agent_label_cursors WHERE agent_label_id = ?;

-- name: LockAgentLabelCursor :one
UPDATE agent_label_cursors
SET last_agent_id = last_agent_id
WHERE agent_label_id = ?
RETURNING *;

-- name: AdvanceAgentLabelCursor :one
UPDATE agent_label_cursors
SET last_agent_id = sqlc.arg(last_agent_id), updated_at = unixepoch()
WHERE agent_label_id = sqlc.arg(agent_label_id)
RETURNING *;

-- name: ClaimScheduledDeploymentOccurrence :one
INSERT INTO scheduled_deployment_occurrences (
    scheduled_deployment_id, due_at, deployment_id
) VALUES (?, ?, ?)
RETURNING *;

-- name: GetScheduledDeploymentOccurrence :one
SELECT * FROM scheduled_deployment_occurrences
WHERE scheduled_deployment_id = sqlc.arg(scheduled_deployment_id)
  AND due_at = sqlc.arg(due_at);

-- name: ListRecoverableScheduledDeployments :many
SELECT deployment.*
FROM scheduled_deployment_occurrences AS occurrence
JOIN deployments AS deployment ON deployment.id = occurrence.deployment_id
LEFT JOIN deployment_dispatches AS dispatch
    ON dispatch.deployment_id = deployment.id
WHERE deployment.status = 'pending'
  AND dispatch.deployment_id IS NULL
ORDER BY occurrence.created_at ASC, deployment.id ASC;
