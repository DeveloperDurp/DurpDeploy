-- name: ListProjectReleaseAvailability :many
WITH ranked_releases AS (
    SELECT r.id, r.project_id, r.version,
        ROW_NUMBER() OVER (PARTITION BY r.project_id
            ORDER BY r.created_at DESC, r.id DESC) AS release_rank
    FROM releases r
    WHERE r.project_id IN (sqlc.slice('project_ids')) AND r.kind = 'deployment'
)
SELECT DISTINCT r.id, r.project_id, r.version, d.environment_id
FROM ranked_releases r LEFT JOIN deployments d ON d.release_id = r.id
AND d.kind = 'deployment' AND d.status = 'succeeded'
AND COALESCE(d.ansible_check_mode, 0) = 0
AND NOT EXISTS (SELECT 1 FROM deployment_steps s
    WHERE d.ansible_check_mode IS NULL AND s.deployment_id = d.id
    AND s.kind = 'ansible' AND s.ansible_json LIKE '%"check":true%')
WHERE r.release_rank = 1;
