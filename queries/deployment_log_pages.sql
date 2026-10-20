-- name: ListDeploymentLogsBefore :many
SELECT id, deployment_id,
    CAST(COALESCE(substr(CAST(step_name AS BLOB), 1, sqlc.arg(preview_limit)), '') AS TEXT) AS step_name,
    CAST(CASE WHEN step_name IS NULL THEN 0 ELSE 1 END AS INTEGER) AS step_name_valid,
    CAST(COALESCE(substr(CAST(line AS BLOB), 1, sqlc.arg(preview_limit)), '') AS TEXT) AS line,
    created_at, step_index,
    CAST(COALESCE(substr(CAST(step_state AS BLOB), 1, sqlc.arg(preview_limit)), '') AS TEXT) AS step_state,
    CAST(CASE WHEN step_state IS NULL THEN 0 ELSE 1 END AS INTEGER) AS step_state_valid,
    is_step_metadata
FROM deployment_logs
WHERE deployment_id = sqlc.arg(deployment_id)
  AND id <= CAST(sqlc.arg(through_id) AS BIGINT)
ORDER BY id DESC LIMIT sqlc.arg(page_limit);

-- name: ListDeploymentLogPreviewsAfter :many
SELECT id, deployment_id,
    CAST(COALESCE(substr(CAST(step_name AS BLOB), 1, sqlc.arg(preview_limit)), '') AS TEXT) AS step_name,
    CAST(CASE WHEN step_name IS NULL THEN 0 ELSE 1 END AS INTEGER) AS step_name_valid,
    CAST(COALESCE(substr(CAST(line AS BLOB), 1, sqlc.arg(preview_limit)), '') AS TEXT) AS line,
    created_at, step_index,
    CAST(COALESCE(substr(CAST(step_state AS BLOB), 1, sqlc.arg(preview_limit)), '') AS TEXT) AS step_state,
    CAST(CASE WHEN step_state IS NULL THEN 0 ELSE 1 END AS INTEGER) AS step_state_valid,
    is_step_metadata
FROM deployment_logs
WHERE deployment_id = sqlc.arg(deployment_id) AND id > sqlc.arg(after_id)
ORDER BY id LIMIT 64;

-- name: ListDeploymentLogStateMetadata :many
SELECT l.id, l.step_index, l.step_name, l.step_state,
    CAST(CASE WHEN l.line = sqlc.arg(queued_cancellation_line)
        THEN sqlc.arg(queued_cancellation_line) ELSE '' END AS TEXT) AS line
FROM deployment_logs l
WHERE l.deployment_id = sqlc.arg(deployment_id) AND l.id <= sqlc.arg(last_id)
  AND l.is_step_metadata = 1 AND l.id IN (
    SELECT MAX(id) FROM deployment_logs
    WHERE deployment_id = sqlc.arg(deployment_id) AND id <= sqlc.arg(last_id)
      AND is_step_metadata = 1
    GROUP BY step_index, step_name
  )
ORDER BY l.id ASC;
