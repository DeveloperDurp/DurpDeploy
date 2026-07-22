-- name: CreateNotificationEvent :one
INSERT INTO notification_events (event_type, deployment_id, project_id, environment_id, message, results)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListNotificationEvents :many
SELECT
    ne.*,
    p.name AS project_name,
    e.name AS environment_name
FROM notification_events ne
LEFT JOIN projects p ON p.id = ne.project_id
LEFT JOIN environments e ON e.id = ne.environment_id
ORDER BY ne.created_at DESC
LIMIT ?;
