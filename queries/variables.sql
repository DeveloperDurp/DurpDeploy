-- name: ListVariablesByProject :many
SELECT * FROM variables WHERE project_id = ? ORDER BY created_at DESC;

-- name: GetVariable :one
SELECT * FROM variables WHERE id = ?;

-- name: CreateVariable :one
INSERT INTO variables (project_id, name, value, environment_id, secret) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateVariable :one
UPDATE variables SET name = ?, value = ?, environment_id = ?, secret = ? WHERE id = ? RETURNING *;

-- name: DeleteVariable :exec
DELETE FROM variables WHERE id = ?;

-- name: ListAllVariables :many
SELECT * FROM variables ORDER BY id;

-- name: UpdateVariableValue :exec
UPDATE variables SET value = ? WHERE id = ?;
