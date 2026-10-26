-- name: ListProjectPanelDeployments :many
WITH ranked AS (
    SELECT d.id, d.status, r.project_id, r.version,
           ROW_NUMBER() OVER (
               PARTITION BY r.project_id, d.environment_id
               ORDER BY d.created_at DESC, d.id DESC
           ) AS recent_rank,
           ROW_NUMBER() OVER (
               PARTITION BY r.project_id, d.environment_id, d.status
               ORDER BY d.created_at DESC, d.id DESC
           ) AS status_rank
    FROM deployments d JOIN releases r ON r.id = d.release_id
    WHERE r.project_id IN (sqlc.slice('project_ids'))
      AND d.environment_id IN (sqlc.slice('environment_ids'))
      AND d.kind = 'deployment'
)
SELECT sqlc.embed(d), ranked.project_id, ranked.version
FROM ranked JOIN deployments d ON d.id = ranked.id
WHERE ranked.recent_rank <= 5
   OR (ranked.status = 'succeeded' AND ranked.status_rank = 1)
ORDER BY ranked.project_id ASC, d.environment_id ASC, d.created_at DESC, d.id DESC;
