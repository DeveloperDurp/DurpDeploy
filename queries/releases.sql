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
      AND (d.status IN ('pending', 'running', 'pending_approval', 'cleanup_unconfirmed')
        OR EXISTS (SELECT 1 FROM remote_deployment_claims c
                   WHERE c.deployment_id = d.id
                     AND c.state IN ('lost', 'cancel_unconfirmed'))
        OR EXISTS (SELECT 1 FROM remote_step_runs s
                   WHERE s.deployment_id = d.id
                     AND s.state IN ('lost', 'cancel_unconfirmed')))
) THEN 1 ELSE 0 END;
