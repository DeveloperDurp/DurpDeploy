-- name: ListLifecycleVariables :many
SELECT * FROM lifecycle_variables WHERE lifecycle_id = ? ORDER BY name, id;

-- name: ListProjectsByLifecycle :many
SELECT id, name FROM projects WHERE lifecycle_id = ? ORDER BY name;

-- name: GetLifecycleVariable :one
SELECT * FROM lifecycle_variables WHERE id = ? AND lifecycle_id = ?;

-- name: CreateLifecycleVariable :one
INSERT INTO lifecycle_variables (lifecycle_id, name, value, environment_id, secret)
VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateLifecycleVariable :one
UPDATE lifecycle_variables SET name = ?, value = ?, environment_id = ?, secret = ?
WHERE id = ? AND lifecycle_id = ? RETURNING *;

-- name: UpdateLifecycleVariableKeepValue :one
UPDATE lifecycle_variables SET name = ?, environment_id = ?, secret = ?
WHERE id = ? AND lifecycle_id = ? RETURNING *;

-- name: DeleteLifecycleVariable :execrows
DELETE FROM lifecycle_variables WHERE id = ? AND lifecycle_id = ?;

-- name: ListAllLifecycleVariables :many
SELECT * FROM lifecycle_variables ORDER BY id;

-- name: UpdateLifecycleVariableValue :exec
UPDATE lifecycle_variables SET value = ? WHERE id = ?;

-- name: LifecycleVariableScopeAllowed :one
SELECT COUNT(*) FROM lifecycle_stages WHERE lifecycle_id = ? AND environment_id = ?;
