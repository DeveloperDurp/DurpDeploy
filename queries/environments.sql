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
