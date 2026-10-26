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
INSERT INTO deployment_logs (deployment_id, step_name, line, is_step_metadata)
VALUES (sqlc.arg(deployment_id), sqlc.narg(step_name), sqlc.arg(line),
    CASE WHEN NOT EXISTS (
        SELECT 1 FROM deployment_logs
        WHERE deployment_id = sqlc.arg(deployment_id) AND is_step_metadata = 1
          AND step_index IS NULL
          AND (step_name = sqlc.narg(step_name)
            OR (step_name IS NULL AND sqlc.narg(step_name) IS NULL))
    ) THEN 1 ELSE 0 END
) RETURNING *;

-- name: CreateStepDeploymentLog :one
INSERT INTO deployment_logs (deployment_id, step_name, line, step_index, step_state, is_step_metadata)
VALUES (sqlc.arg(deployment_id), sqlc.narg(step_name), sqlc.arg(line),
    sqlc.narg(step_index), sqlc.narg(step_state),
    CASE WHEN CAST(sqlc.narg(step_state) AS TEXT) IS NOT NULL OR NOT EXISTS (
        SELECT 1 FROM deployment_logs
        WHERE deployment_id = sqlc.arg(deployment_id) AND is_step_metadata = 1
          AND (step_index = sqlc.narg(step_index)
            OR (step_index IS NULL AND sqlc.narg(step_index) IS NULL))
          AND (step_name = sqlc.narg(step_name)
            OR (step_name IS NULL AND sqlc.narg(step_name) IS NULL))
    ) THEN 1 ELSE 0 END
) RETURNING *;

-- name: LockDeploymentLogStream :execrows
UPDATE deployment_step_sources SET steps_json = steps_json -- NOSONAR: intentional write lock
WHERE deployment_id = ?;

-- name: ListDeploymentLogsAfter :many
SELECT * FROM deployment_logs WHERE deployment_id = ? AND id > ?
ORDER BY id LIMIT sqlc.arg(batch_limit);

-- name: CreateRemoteDeploymentLog :one
INSERT INTO deployment_logs (deployment_id, step_name, line, created_at, is_step_metadata)
VALUES (sqlc.arg(deployment_id), NULL, sqlc.arg(line), sqlc.arg(created_at),
    CASE WHEN NOT EXISTS (
        SELECT 1 FROM deployment_logs
        WHERE deployment_id = sqlc.arg(deployment_id) AND is_step_metadata = 1
          AND step_index IS NULL AND step_name IS NULL
    ) THEN 1 ELSE 0 END
) RETURNING *;

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

-- name: GetLastRemoteDeploymentLogSequence :one
SELECT CAST(COALESCE(MAX(sequence), -1) AS INTEGER) FROM deployment_log_scopes
WHERE deployment_id = ? AND step_index IS NULL;

-- name: UpdateRemoteDeploymentLogBuffer :execrows
UPDATE remote_deployment_claims
SET log_buffer_ciphertext = sqlc.narg(log_buffer_ciphertext)
WHERE deployment_id = sqlc.arg(deployment_id)
  AND agent_id = sqlc.arg(agent_id)
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: CreateDeploymentLogScope :exec
INSERT INTO deployment_log_scopes (log_id, deployment_id, step_index, attempt, sequence)
VALUES (?, ?, ?, ?, ?);
