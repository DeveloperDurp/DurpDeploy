-- name: ListEnvironments :many
SELECT * FROM environments ORDER BY created_at DESC;

-- name: ListDeploymentEnvironmentsForUser :many
SELECT e.* FROM environments e
WHERE EXISTS (
    SELECT 1 FROM deployments d
    JOIN releases r ON r.id = d.release_id
    JOIN project_members pm ON pm.project_id = r.project_id
    WHERE d.environment_id = e.id AND d.kind = 'deployment'
      AND pm.user_id = sqlc.arg(user_id)
)
ORDER BY e.created_at DESC;

-- name: ListEnvironmentsPaginated :many
SELECT * FROM environments ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountEnvironments :one
SELECT COUNT(*) FROM environments;

-- name: GetEnvironment :one
SELECT * FROM environments WHERE id = ?;

-- name: CreateEnvironment :one
INSERT INTO environments (name, description, tags, verification_type, verification_target, verification_timeout_seconds)
VALUES (sqlc.arg(name), sqlc.narg(description), sqlc.narg(tags), sqlc.arg(verification_type), sqlc.arg(verification_target), COALESCE(NULLIF(CAST(sqlc.arg(verification_timeout_seconds) AS INTEGER), 0), 30)) RETURNING *;

-- name: UpdateEnvironment :one
UPDATE environments SET name = sqlc.arg(name), description = sqlc.narg(description), tags = sqlc.narg(tags),
verification_type = CASE WHEN CAST(sqlc.arg(configure_verification) AS INTEGER) = 1 THEN sqlc.arg(verification_type) ELSE verification_type END,
verification_target = CASE WHEN CAST(sqlc.arg(configure_verification) AS INTEGER) = 1 THEN sqlc.arg(verification_target) ELSE verification_target END,
verification_timeout_seconds = CASE WHEN CAST(sqlc.arg(configure_verification) AS INTEGER) = 1 THEN COALESCE(NULLIF(CAST(sqlc.arg(verification_timeout_seconds) AS INTEGER), 0), 30) ELSE verification_timeout_seconds END
WHERE id = sqlc.arg(id) RETURNING *;

-- name: DeleteEnvironment :exec
DELETE FROM environments WHERE id = ?;

-- name: ListEnvironmentDeploymentIDs :many
SELECT id FROM deployments WHERE environment_id = ?;

-- name: HasActiveEnvironmentDeployment :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM deployments d WHERE d.environment_id = ?
      AND (d.status IN ('queued', 'pending', 'running', 'pending_approval', 'publishing_artifact', 'awaiting_artifact_approval')
        OR (d.status = 'cleanup_unconfirmed'
            AND (d.container_namespace IS NOT NULL OR (
            NOT EXISTS (SELECT 1 FROM remote_step_runs s
                WHERE s.deployment_id = d.id AND s.state = 'cleanup_unconfirmed')
            AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c
                WHERE c.deployment_id = d.id AND c.state = 'cleanup_unconfirmed'))))
        OR EXISTS (SELECT 1 FROM remote_deployment_claims c
                   WHERE c.deployment_id = d.id
                     AND (c.state IN ('lost', 'cancel_unconfirmed')
                       OR (c.state = 'cleanup_unconfirmed'
                           AND c.cleanup_confirmed_at IS NULL)))
        OR EXISTS (SELECT 1 FROM remote_step_runs s
                   WHERE s.deployment_id = d.id
                     AND (s.state IN ('lost', 'cancel_unconfirmed')
                       OR (s.state = 'cleanup_unconfirmed'
                           AND s.cleanup_confirmed_at IS NULL))))
) THEN 1 ELSE 0 END;
