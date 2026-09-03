-- name: ListDeploymentLogsByDeployment :many
SELECT * FROM deployment_logs WHERE deployment_id = ? ORDER BY created_at DESC;

-- name: GetDeploymentLog :one
SELECT * FROM deployment_logs WHERE id = ?;

-- name: CreateDeploymentLog :one
INSERT INTO deployment_logs (deployment_id, step_name, line, sequence)
SELECT ?1, ?2, ?3, COALESCE(MAX(sequence), 0) + 1
FROM deployment_logs
WHERE deployment_id = ?1
RETURNING *;

-- name: CreateSequencedDeploymentLogForDispatch :one
INSERT INTO deployment_logs (deployment_id, step_name, line, sequence)
SELECT
    sqlc.arg(deployment_id),
    sqlc.narg(step_name),
    sqlc.arg(line),
    sqlc.arg(sequence)
WHERE EXISTS (
    SELECT 1
    FROM deployment_dispatches
    WHERE deployment_id = sqlc.arg(deployment_id)
      AND agent_id = sqlc.arg(agent_id)
      AND claim_token_hash = sqlc.arg(claim_token_hash)
      AND state = sqlc.arg(current_state)
)
ON CONFLICT(deployment_id, sequence) DO UPDATE
SET deployment_id = deployment_logs.deployment_id
RETURNING *;

-- name: UpdateDeploymentLog :one
UPDATE deployment_logs SET deployment_id = ?, step_name = ?, line = ? WHERE id = ? RETURNING *;

-- name: DeleteDeploymentLog :exec
DELETE FROM deployment_logs WHERE id = ?;
