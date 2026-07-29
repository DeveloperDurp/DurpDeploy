-- name: ListProjects :many
SELECT * FROM projects ORDER BY created_at DESC;

-- name: ListProjectsPaginated :many
SELECT * FROM projects ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountProjects :one
SELECT COUNT(*) FROM projects;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: CreateProject :one
INSERT INTO projects (name, description) VALUES (?, ?) RETURNING *;

-- name: UpdateProject :one
UPDATE projects SET name = ?, description = ? WHERE id = ? RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ?;

-- name: SetProjectLifecycle :exec
UPDATE projects SET lifecycle_id = ? WHERE id = ?;

-- name: ClearProjectLifecycle :exec
UPDATE projects SET lifecycle_id = NULL WHERE id = ?;

-- name: UpdateProjectNotifications :exec
UPDATE projects SET slack_webhook_url = ?, notify_emails = ?, gotify_url = ?, gotify_token = ?, discord_webhook_url = ? WHERE id = ?;
