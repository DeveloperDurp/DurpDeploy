-- name: ListDeploymentLogsByDeployment :many
SELECT l.* FROM deployment_logs l
LEFT JOIN deployment_log_scopes s ON s.log_id = l.id
WHERE l.deployment_id = ?
ORDER BY CASE
    WHEN s.step_index IS NULL AND s.attempt IS NULL THEN 0 ELSE 1
END,
CASE
    WHEN s.step_index IS NULL AND s.attempt IS NULL THEN s.sequence
END,
l.created_at DESC, l.id DESC;

-- name: GetDeploymentLog :one
SELECT * FROM deployment_logs WHERE id = ?;

-- name: CreateDeploymentLog :one
INSERT INTO deployment_logs (deployment_id, step_name, line) VALUES (?, ?, ?) RETURNING *;

-- name: CreateRemoteDeploymentLog :one
INSERT INTO deployment_logs (deployment_id, step_name, line, created_at)
VALUES (?, NULL, ?, ?) RETURNING *;

-- name: UpdateDeploymentLog :one
UPDATE deployment_logs SET deployment_id = ?, step_name = ?, line = ? WHERE id = ? RETURNING *;

-- name: DeleteDeploymentLog :exec
DELETE FROM deployment_logs WHERE id = ?;

-- name: GetScopedDeploymentLog :one
SELECT l.* FROM deployment_logs l JOIN deployment_log_scopes s ON s.log_id = l.id AND s.deployment_id = l.deployment_id
WHERE s.deployment_id = ? AND s.step_index = ? AND s.attempt = ? AND s.sequence = ?;

-- name: GetRemoteDeploymentLogBySequence :one
SELECT l.* FROM deployment_logs l
JOIN deployment_log_scopes s ON s.log_id = l.id AND s.deployment_id = l.deployment_id
WHERE s.deployment_id = ? AND s.step_index IS NULL AND s.attempt IS NULL
    AND s.sequence = ?;

-- name: CreateDeploymentLogScope :exec
INSERT INTO deployment_log_scopes (log_id, deployment_id, step_index, attempt, sequence)
VALUES (?, ?, ?, ?, ?);
