-- name: ListEnvironments :many
SELECT * FROM environments ORDER BY created_at DESC;

-- name: ListEnvironmentsPaginated :many
SELECT * FROM environments ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountEnvironments :one
SELECT COUNT(*) FROM environments;

-- name: GetEnvironment :one
SELECT * FROM environments WHERE id = ?;

-- name: CreateEnvironment :one
INSERT INTO environments (name, description, tags) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateEnvironment :one
UPDATE environments SET name = ?, description = ?, tags = ? WHERE id = ? RETURNING *;

-- name: DeleteEnvironment :exec
DELETE FROM environments WHERE id = ?;

-- name: ListEnvironmentDeploymentIDs :many
SELECT id FROM deployments WHERE environment_id = ?;

-- name: HasActiveEnvironmentDeployment :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM deployments d WHERE d.environment_id = ?
      AND (d.status IN ('pending', 'running', 'pending_approval')
        OR EXISTS (SELECT 1 FROM remote_deployment_claims c
                   WHERE c.deployment_id = d.id
                     AND c.state IN ('lost', 'cancel_unconfirmed'))
        OR EXISTS (SELECT 1 FROM remote_step_runs s
                   WHERE s.deployment_id = d.id
                     AND s.state IN ('lost', 'cancel_unconfirmed')))
) THEN 1 ELSE 0 END;
