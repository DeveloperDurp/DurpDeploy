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

-- name: CountDeploymentsToday :one
SELECT COUNT(*) FROM deployments WHERE created_at >= strftime('%s','now','start of day');

-- name: ListRunningDeploymentsWithRefs :many
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
WHERE d.status IN ('pending','running')
ORDER BY d.created_at DESC;

-- name: ListLatestDeploymentPerReleaseEnv :many
SELECT id, release_id, environment_id, status, started_at, finished_at, created_at, forced, note, project_name, release_version, environment_name
FROM (
    SELECT d.id, d.release_id, d.environment_id, d.status, d.started_at, d.finished_at, d.created_at, d.forced, d.note,
           p.name AS project_name, r.version AS release_version, e.name AS environment_name,
           ROW_NUMBER() OVER (PARTITION BY d.release_id, d.environment_id ORDER BY d.created_at DESC) AS rn
    FROM deployments d
    JOIN releases r ON d.release_id = r.id
    JOIN projects p ON r.project_id = p.id
    JOIN environments e ON d.environment_id = e.id
) WHERE rn = 1 ORDER BY created_at DESC;

-- name: ListDeploymentsWithRefsFiltered :many
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
WHERE (CAST(sqlc.narg(f_project_id) AS INTEGER) IS NULL OR d.release_id IN (SELECT id FROM releases WHERE project_id = CAST(sqlc.narg(f_project_id) AS INTEGER)))
  AND (CAST(sqlc.narg(f_env_id)     AS INTEGER) IS NULL OR d.environment_id = CAST(sqlc.narg(f_env_id) AS INTEGER))
  AND (CAST(sqlc.narg(f_status)     AS TEXT)    IS NULL OR d.status = CAST(sqlc.narg(f_status) AS TEXT))
  AND (CAST(sqlc.narg(f_from_unix)  AS INTEGER) IS NULL OR d.created_at >= CAST(sqlc.narg(f_from_unix) AS INTEGER))
  AND (CAST(sqlc.narg(f_to_unix)    AS INTEGER) IS NULL OR d.created_at <= CAST(sqlc.narg(f_to_unix) AS INTEGER))
ORDER BY d.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountDeploymentsWithRefsFiltered :one
SELECT COUNT(*)
FROM deployments d
JOIN releases r ON d.release_id = r.id
JOIN projects p ON r.project_id = p.id
JOIN environments e ON d.environment_id = e.id
WHERE (CAST(sqlc.narg(f_project_id) AS INTEGER) IS NULL OR d.release_id IN (SELECT id FROM releases WHERE project_id = CAST(sqlc.narg(f_project_id) AS INTEGER)))
  AND (CAST(sqlc.narg(f_env_id)     AS INTEGER) IS NULL OR d.environment_id = CAST(sqlc.narg(f_env_id) AS INTEGER))
  AND (CAST(sqlc.narg(f_status)     AS TEXT)    IS NULL OR d.status = CAST(sqlc.narg(f_status) AS TEXT))
  AND (CAST(sqlc.narg(f_from_unix)  AS INTEGER) IS NULL OR d.created_at >= CAST(sqlc.narg(f_from_unix) AS INTEGER))
  AND (CAST(sqlc.narg(f_to_unix)    AS INTEGER) IS NULL OR d.created_at <= CAST(sqlc.narg(f_to_unix) AS INTEGER));
