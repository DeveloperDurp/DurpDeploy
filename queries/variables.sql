-- name: ListVariablesByProject :many
SELECT * FROM variables WHERE project_id = ? ORDER BY created_at DESC;

-- name: ListVariablesByProjectPaginated :many
SELECT * FROM variables
WHERE project_id = sqlc.arg(project_id)
  AND (CAST(sqlc.narg('f_environment_id') AS BIGINT) IS NULL OR environment_id = sqlc.narg('f_environment_id'))
  AND (CAST(sqlc.narg('f_secret_only') AS BIGINT) IS NULL OR secret = sqlc.narg('f_secret_only'))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountVariablesByProject :one
SELECT COUNT(*) FROM variables
WHERE project_id = sqlc.arg(project_id)
  AND (CAST(sqlc.narg('f_environment_id') AS BIGINT) IS NULL OR environment_id = sqlc.narg('f_environment_id'))
  AND (CAST(sqlc.narg('f_secret_only') AS BIGINT) IS NULL OR secret = sqlc.narg('f_secret_only'));

-- name: GetVariable :one
SELECT * FROM variables WHERE id = ?;

-- name: CreateVariable :one
INSERT INTO variables (project_id, name, value, environment_id, secret, is_credential, expires_at, rotation_due_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateVariable :one
UPDATE variables SET name = ?, value = ?, environment_id = ?, secret = ?, rotation_revision = rotation_revision + 1 WHERE id = ? AND is_credential = 0 RETURNING *;

-- name: UpdateVariableKeepValue :one
UPDATE variables SET name = ?, environment_id = ?, secret = ?, rotation_revision = rotation_revision + 1 WHERE id = ? AND is_credential = 0 RETURNING *;

-- name: DeleteVariable :exec
DELETE FROM variables WHERE id = ? AND is_credential = 0;

-- name: DeleteVariableOverrides :exec
DELETE FROM variables
WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name) AND is_credential = 0
  AND (environment_id = sqlc.narg(environment_id)
       OR (environment_id IS NULL AND sqlc.narg(environment_id) IS NULL));

-- name: ListAllVariables :many
SELECT * FROM variables ORDER BY id;

-- name: UpdateVariableValue :exec
UPDATE variables SET value = ? WHERE id = ?;
