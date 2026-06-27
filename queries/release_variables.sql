-- name: ListReleaseVariablesByRelease :many
SELECT * FROM release_variables WHERE release_id = ? ORDER BY created_at DESC;

-- name: GetReleaseVariable :one
SELECT * FROM release_variables WHERE id = ?;

-- name: CreateReleaseVariable :one
INSERT INTO release_variables (release_id, name, value, environment_id) VALUES (?, ?, ?, ?) RETURNING *;

-- name: UpdateReleaseVariable :one
UPDATE release_variables SET release_id = ?, name = ?, value = ?, environment_id = ? WHERE id = ? RETURNING *;

-- name: DeleteReleaseVariable :exec
DELETE FROM release_variables WHERE id = ?;

-- name: DeleteReleaseVariablesByRelease :exec
DELETE FROM release_variables WHERE release_id = ?;
