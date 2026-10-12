-- name: ListDeploymentLogsBefore :many
SELECT * FROM deployment_logs
WHERE deployment_id = sqlc.arg(deployment_id)
  AND (CAST(sqlc.arg(before_id) AS BIGINT) = 0 OR id < CAST(sqlc.arg(before_id) AS BIGINT))
ORDER BY id DESC LIMIT sqlc.arg(page_limit);

-- name: ListDeploymentLogStateMetadata :many
SELECT l.id, l.step_index, l.step_name, l.step_state FROM deployment_logs l
WHERE l.deployment_id = sqlc.arg(deployment_id) AND l.id <= sqlc.arg(last_id)
  AND l.is_step_metadata = 1 AND l.id IN (
    SELECT MAX(id) FROM deployment_logs
    WHERE deployment_id = sqlc.arg(deployment_id) AND id <= sqlc.arg(last_id)
      AND is_step_metadata = 1
    GROUP BY step_index, step_name
  )
ORDER BY l.id;
