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
INSERT INTO steps (project_id, name, script_body, sort_order, timeout_seconds, max_retries, interpreter, agent_execution_mode, container_image, variable_names, network_mode, approval_artifact_path, approval_review_path, approval_review_format)
VALUES (?, ?, ?, ?, ?, ?, COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash'), COALESCE(NULLIF(CAST(sqlc.arg(agent_execution_mode) AS TEXT), ''), 'host'), sqlc.arg(container_image), sqlc.arg(variable_names), sqlc.arg(network_mode), sqlc.arg(approval_artifact_path), sqlc.arg(approval_review_path), sqlc.arg(approval_review_format))
RETURNING *;

-- name: UpdateStep :one
UPDATE steps SET name = ?, script_body = ?, sort_order = ?, timeout_seconds = ?, max_retries = ?,
interpreter = COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash'),
agent_execution_mode = COALESCE(NULLIF(CAST(sqlc.arg(agent_execution_mode) AS TEXT), ''), 'host'),
container_image = sqlc.arg(container_image), variable_names = sqlc.arg(variable_names), network_mode = sqlc.arg(network_mode), approval_artifact_path = sqlc.arg(approval_artifact_path), approval_review_path = sqlc.arg(approval_review_path), approval_review_format = sqlc.arg(approval_review_format)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: DeleteStep :exec
DELETE FROM steps WHERE id = ?;
