-- name: ListDeploymentLogsByDeployment :many
SELECT * FROM deployment_logs WHERE deployment_id = ? ORDER BY created_at DESC;

-- name: GetDeploymentLog :one
SELECT * FROM deployment_logs WHERE id = ?;

-- name: CreateDeploymentLog :one
INSERT INTO deployment_logs (deployment_id, step_name, line) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateDeploymentLog :one
UPDATE deployment_logs SET deployment_id = ?, step_name = ?, line = ? WHERE id = ? RETURNING *;

-- name: DeleteDeploymentLog :exec
DELETE FROM deployment_logs WHERE id = ?;

-- name: GetScopedDeploymentLog :one
SELECT l.* FROM deployment_logs l JOIN deployment_log_scopes s ON s.log_id = l.id AND s.deployment_id = l.deployment_id
WHERE s.deployment_id = ? AND s.step_index = ? AND s.attempt = ? AND s.sequence = ?;

-- name: CreateDeploymentLogScope :exec
INSERT INTO deployment_log_scopes (log_id, deployment_id, step_index, attempt, sequence)
VALUES (?, ?, ?, ?, ?);
