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
    SELECT 1 FROM deployments WHERE environment_id = ?
      AND status IN ('pending', 'running', 'pending_approval')
) THEN 1 ELSE 0 END;
