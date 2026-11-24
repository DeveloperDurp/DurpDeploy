-- name: ListVariablesByProject :many
SELECT * FROM variables WHERE project_id = ? ORDER BY created_at DESC;

-- name: ListVariablesByProjectPaginated :many
WITH variable_sort AS (
  SELECT CAST(sqlc.arg(sort_by_name) AS INTEGER) AS by_name
)
SELECT variables.* FROM variables CROSS JOIN variable_sort
WHERE project_id = sqlc.arg(project_id)
  AND (CAST(sqlc.narg('f_environment_id') AS BIGINT) IS NULL OR environment_id = sqlc.narg('f_environment_id'))
  AND (CAST(sqlc.narg('f_secret_only') AS BIGINT) IS NULL OR secret = sqlc.narg('f_secret_only'))
ORDER BY CASE WHEN by_name = 1 THEN name END COLLATE BINARY,
         CASE WHEN by_name = 1 THEN COALESCE(environment_id, 0) END,
         created_at DESC,
         CASE WHEN by_name = 1 THEN id END DESC
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
SELECT id, project_id, name, value, environment_id, created_at, secret, expires_at, rotation_due_at, rotated_at, rotation_revision, is_credential FROM variables ORDER BY id ASC;

-- name: UpdateVariableValue :exec
UPDATE variables SET value = ? WHERE id = ?;

-- name: UpdateVariableGroupValue :execrows
UPDATE variables
SET value = sqlc.arg(value), rotation_revision = rotation_revision + 1
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id)
  AND environment_id IS NOT NULL AND is_credential = 0
  AND rotation_revision = sqlc.arg(revision);
