-- name: ListVariablesByProject :many
SELECT * FROM variables WHERE project_id = ? ORDER BY created_at DESC;

-- name: ListVariablesByProjectPaginated :many
SELECT * FROM variables
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg('f_environment_id') IS NULL OR environment_id = sqlc.narg('f_environment_id'))
  AND (sqlc.narg('f_secret_only') IS NULL OR secret = sqlc.narg('f_secret_only'))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountVariablesByProject :one
SELECT COUNT(*) FROM variables
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg('f_environment_id') IS NULL OR environment_id = sqlc.narg('f_environment_id'))
  AND (sqlc.narg('f_secret_only') IS NULL OR secret = sqlc.narg('f_secret_only'));

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
