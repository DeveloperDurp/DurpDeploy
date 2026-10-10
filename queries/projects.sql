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

-- name: LockProject :execrows
UPDATE projects SET name = name WHERE id = ?; -- NOSONAR: intentional write lock

-- name: SetProjectLifecycle :exec
UPDATE projects SET lifecycle_id = ? WHERE id = ?;

-- name: ClearProjectLifecycle :exec
UPDATE projects SET lifecycle_id = NULL WHERE id = ?;

-- name: ClearProjectLifecycleIfAssigned :execrows
UPDATE projects SET lifecycle_id = NULL WHERE id = ? AND lifecycle_id = ?;

-- name: UpdateProjectNotifications :exec
UPDATE projects SET slack_webhook_url = ?, notify_emails = ?, gotify_url = ?, gotify_token = ?, discord_webhook_url = ? WHERE id = ?;

-- name: ListProjectDeploymentIDs :many
SELECT d.id FROM deployments d JOIN releases r ON r.id = d.release_id
WHERE r.project_id = ?;

-- name: HasActiveProjectDeployment :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM deployments d JOIN releases r ON r.id = d.release_id
    WHERE r.project_id = sqlc.arg(project_id)
      AND (d.status IN ('queued', 'pending', 'running', 'pending_approval', 'publishing_artifact', 'awaiting_artifact_approval')
        OR (d.status = 'cleanup_unconfirmed'
            AND (d.container_namespace IS NOT NULL OR (
            d.cleanup_confirmed_at IS NULL AND
            NOT EXISTS (SELECT 1 FROM remote_step_runs s
                WHERE s.deployment_id = d.id AND s.state = 'cleanup_unconfirmed')
            AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c
                WHERE c.deployment_id = d.id AND c.state = 'cleanup_unconfirmed'))))
        OR EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = d.id
            AND (c.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed')
                OR (c.state = 'cleanup_unconfirmed' AND c.cleanup_confirmed_at IS NULL)))
        OR EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = d.id
            AND (s.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed')
                OR (s.state = 'cleanup_unconfirmed' AND s.cleanup_confirmed_at IS NULL))))
) THEN 1 ELSE 0 END;
