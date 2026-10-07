-- name: GetLatestProjectEnvironmentDeployment :one
SELECT d.* FROM deployments d JOIN releases r ON r.id = d.release_id
WHERE r.project_id = ? AND d.environment_id = ? AND d.kind = 'deployment'
ORDER BY d.created_at DESC, d.id DESC LIMIT 1;

-- name: GetRollbackTarget :one
SELECT d.* FROM deployments d JOIN releases r ON r.id = d.release_id
JOIN deployments source ON source.id = sqlc.arg(source_deployment_id)
JOIN releases source_release ON source_release.id = source.release_id
WHERE r.project_id = source_release.project_id
  AND d.environment_id = source.environment_id
  AND d.kind = 'deployment' AND d.status = 'succeeded'
  AND d.release_id != source.release_id
  AND (d.created_at < source.created_at OR (d.created_at = source.created_at AND d.id < source.id))
ORDER BY d.created_at DESC, d.id DESC LIMIT 1;

-- name: HasActiveProjectEnvironmentDeployment :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM deployments d JOIN releases r ON r.id = d.release_id
    WHERE r.project_id = ? AND d.environment_id = ? AND d.kind = 'deployment'
      AND (d.status IN ('queued', 'pending', 'running', 'pending_approval', 'publishing_artifact', 'awaiting_artifact_approval')
        OR (d.status = 'cleanup_unconfirmed'
            AND (d.container_namespace IS NOT NULL OR (
            d.cleanup_confirmed_at IS NULL AND
            NOT EXISTS (SELECT 1 FROM remote_step_runs s
                WHERE s.deployment_id = d.id AND s.state = 'cleanup_unconfirmed')
            AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c
                WHERE c.deployment_id = d.id AND c.state = 'cleanup_unconfirmed'))))
        OR EXISTS (SELECT 1 FROM remote_deployment_claims c
                   WHERE c.deployment_id = d.id
                     AND (c.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed')
                       OR (c.state = 'cleanup_unconfirmed'
                           AND c.cleanup_confirmed_at IS NULL)))
        OR EXISTS (SELECT 1 FROM remote_step_runs s
                   WHERE s.deployment_id = d.id
                     AND (s.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed')
                       OR (s.state = 'cleanup_unconfirmed'
                           AND s.cleanup_confirmed_at IS NULL))))
) THEN 1 ELSE 0 END;

-- name: CreateDeploymentRollback :exec
INSERT INTO deployment_rollbacks (deployment_id, source_deployment_id, target_deployment_id, source_version, target_version)
VALUES (?, ?, ?, ?, ?);

-- name: GetDeploymentRollback :one
SELECT * FROM deployment_rollbacks WHERE deployment_id = ?;
