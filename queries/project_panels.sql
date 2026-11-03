-- name: ListProjectPanelDeployments :many
WITH runs AS (
    SELECT d.id, d.status, d.environment_id, d.created_at, r.project_id, r.version,
           CAST(CASE WHEN d.ansible_check_mode = 1 OR (d.ansible_check_mode IS NULL AND EXISTS (
               SELECT 1 FROM deployment_steps s WHERE s.deployment_id = d.id
                 AND s.kind = 'ansible' AND s.ansible_json LIKE '%"check":true%'
           )) THEN 1 ELSE 0 END AS INTEGER) AS is_check
    FROM deployments d JOIN releases r ON r.id = d.release_id
    WHERE r.project_id IN (sqlc.slice('project_ids'))
      AND d.environment_id IN (sqlc.slice('environment_ids'))
      AND d.kind = 'deployment'
), ranked AS (
    SELECT runs.*,
           ROW_NUMBER() OVER (
               PARTITION BY project_id, environment_id
               ORDER BY created_at DESC, id DESC
           ) AS recent_rank,
           ROW_NUMBER() OVER (
               PARTITION BY project_id, environment_id, status, is_check
               ORDER BY created_at DESC, id DESC
           ) AS status_rank
    FROM runs
)
SELECT sqlc.embed(d), ranked.project_id, ranked.version, ranked.is_check
FROM ranked JOIN deployments d ON d.id = ranked.id
WHERE ranked.recent_rank <= 5
   OR (ranked.status = 'succeeded' AND ranked.is_check = 0 AND ranked.status_rank = 1)
ORDER BY ranked.project_id ASC, d.environment_id ASC, d.created_at DESC, d.id DESC;
