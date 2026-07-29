-- name: ListStepsByProject :many
SELECT * FROM steps WHERE project_id = ? ORDER BY sort_order ASC, created_at ASC;

-- name: ListStepsByProjectPaginated :many
SELECT * FROM steps WHERE project_id = ? ORDER BY sort_order ASC, created_at ASC
LIMIT ? OFFSET ?;

-- name: CountStepsByProject :one
SELECT COUNT(*) FROM steps WHERE project_id = ?;

-- name: GetStep :one
SELECT * FROM steps WHERE id = ?;

-- name: CreateStep :one
INSERT INTO steps (project_id, name, script_body, sort_order, timeout_seconds, max_retries) VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateStep :one
UPDATE steps SET name = ?, script_body = ?, sort_order = ?, timeout_seconds = ?, max_retries = ? WHERE id = ? RETURNING *;

-- name: DeleteStep :exec
DELETE FROM steps WHERE id = ?;
