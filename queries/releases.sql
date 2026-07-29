-- name: ListReleasesByProject :many
SELECT * FROM releases WHERE project_id = ? ORDER BY created_at DESC;

-- name: ListReleasesByProjectPaginated :many
SELECT * FROM releases WHERE project_id = ? ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountReleasesByProject :one
SELECT COUNT(*) FROM releases WHERE project_id = ?;

-- name: GetRelease :one
SELECT * FROM releases WHERE id = ?;

-- name: CreateRelease :one
INSERT INTO releases (project_id, version, steps_json) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateRelease :one
UPDATE releases SET project_id = ?, version = ?, steps_json = ? WHERE id = ? RETURNING *;

-- name: DeleteRelease :exec
DELETE FROM releases WHERE id = ?;
