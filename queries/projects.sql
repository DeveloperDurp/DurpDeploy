-- name: ListProjects :many
SELECT * FROM projects ORDER BY created_at DESC;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: CreateProject :one
INSERT INTO projects (name, description) VALUES (?, ?) RETURNING *;

-- name: UpdateProject :one
UPDATE projects SET name = ?, description = ? WHERE id = ? RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ?;

-- name: SetProjectLifecycle :exec
UPDATE projects SET lifecycle_id = ? WHERE id = ?;

-- name: ClearProjectLifecycle :exec
UPDATE projects SET lifecycle_id = NULL WHERE id = ?;
