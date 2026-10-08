-- name: ListReleasesByProject :many
SELECT * FROM releases WHERE project_id = ? AND kind = 'deployment' ORDER BY created_at DESC;

-- name: ListReleasesByProjectPaginated :many
SELECT * FROM releases WHERE project_id = ? AND kind = 'deployment' ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountReleasesByProject :one
SELECT COUNT(*) FROM releases WHERE project_id = ? AND kind = 'deployment';

-- name: GetRelease :one
SELECT * FROM releases WHERE id = ?;

-- name: GetDeploymentRelease :one
SELECT * FROM releases WHERE id = ? AND kind = 'deployment';

-- name: LockRelease :execrows
UPDATE releases SET version = version -- NOSONAR: intentional write lock
WHERE id = ?;

-- name: CreateRelease :one
INSERT INTO releases (project_id, version, steps_json) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateRelease :one
UPDATE releases SET project_id = ?, version = ?, steps_json = ? WHERE id = ? RETURNING *;

-- name: DeleteRelease :exec
DELETE FROM releases
WHERE id = ? AND project_id = ? AND kind = 'deployment';

-- name: HasActiveReleaseDeployment :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM deployments d WHERE d.release_id = ?
      AND (d.status IN ('queued', 'pending', 'running', 'pending_approval', 'publishing_artifact', 'awaiting_artifact_approval')
        OR (d.status = 'cleanup_unconfirmed'
            AND (d.container_namespace IS NOT NULL OR (
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

-- name: HasUnflushedReleaseLogs :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM deployments d WHERE d.release_id = ?
      AND (EXISTS (SELECT 1 FROM remote_deployment_claims c
                   WHERE c.deployment_id = d.id
                     AND COALESCE(c.log_buffer_ciphertext, '') <> '')
        OR EXISTS (SELECT 1 FROM remote_step_runs s
                   WHERE s.deployment_id = d.id
                     AND COALESCE(s.log_buffer_ciphertext, '') <> ''))
) THEN 1 ELSE 0 END;
