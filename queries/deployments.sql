-- name: ListDeploymentsByRelease :many
SELECT * FROM deployments WHERE release_id = ? ORDER BY created_at DESC;

-- name: GetDeployment :one
SELECT * FROM deployments WHERE id = ?;

-- name: CreateDeployment :one
INSERT INTO deployments (release_id, environment_id, status, started_at, finished_at, forced, note) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateDeployment :one
UPDATE deployments SET release_id = ?, environment_id = ?, status = ?, started_at = ?, finished_at = ?, note = ? WHERE id = ? RETURNING *;

-- name: UpdateDeploymentStatus :exec
UPDATE deployments SET status = ?, started_at = ?, finished_at = ? WHERE id = ?;

-- name: ListDeployments :many
SELECT * FROM deployments ORDER BY created_at DESC;

-- name: ListDeploymentsWithRefs :many
SELECT
    d.id, d.release_id, d.environment_id, d.status,
    d.started_at, d.finished_at, d.created_at, d.forced, d.note,
    p.name AS project_name,
    r.version AS release_version,
    e.name AS environment_name
FROM deployments d
JOIN releases r ON d.release_id = r.id
JOIN projects p ON r.project_id = p.id
JOIN environments e ON d.environment_id = e.id
ORDER BY d.created_at DESC;

-- name: ListRecentDeployments :many
SELECT * FROM deployments ORDER BY created_at DESC LIMIT ?;

-- name: DeleteDeployment :exec
DELETE FROM deployments WHERE id = ?;

-- name: GetLatestDeploymentForReleaseEnv :one
SELECT * FROM deployments WHERE release_id = ? AND environment_id = ? ORDER BY created_at DESC LIMIT 1;

-- name: GetLatestSuccessfulDeploymentForReleaseEnv :one
SELECT * FROM deployments WHERE release_id = ? AND environment_id = ? AND status = 'succeeded' ORDER BY created_at DESC LIMIT 1;

-- name: GetLatestSuccessfulDeploymentForEnv :one
SELECT * FROM deployments WHERE environment_id = ? AND status = 'succeeded' ORDER BY created_at DESC LIMIT 1;

-- name: ListRecentDeploymentsForEnv :many
SELECT * FROM deployments WHERE environment_id = ? ORDER BY created_at DESC LIMIT ?;
