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

-- name: DeleteRelease :execrows
DELETE FROM releases
WHERE releases.id = ? AND releases.project_id = ? AND releases.kind = 'deployment'
  AND NOT EXISTS (SELECT 1 FROM deployments WHERE release_id = releases.id)
  AND NOT EXISTS (SELECT 1 FROM scheduled_deployments WHERE release_id = releases.id);
