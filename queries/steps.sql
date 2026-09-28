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
INSERT INTO steps (project_id, name, script_body, sort_order, timeout_seconds, max_retries, interpreter, container_image, variable_names)
VALUES (?, ?, ?, ?, ?, ?, COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash'), sqlc.arg(container_image), sqlc.arg(variable_names))
RETURNING *;

-- name: UpdateStep :one
UPDATE steps SET name = ?, script_body = ?, sort_order = ?, timeout_seconds = ?, max_retries = ?,
interpreter = COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash'),
container_image = sqlc.arg(container_image), variable_names = sqlc.arg(variable_names)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: DeleteStep :exec
DELETE FROM steps WHERE id = ?;
