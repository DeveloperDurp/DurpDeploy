-- name: CreateProjectGroup :one
INSERT INTO project_groups (name, name_key) VALUES (?, ?) RETURNING *;

-- name: UpdateProjectGroup :one
UPDATE project_groups SET name = ?, name_key = ? WHERE id = ? RETURNING *;

-- name: DeleteProjectGroup :execrows
DELETE FROM project_groups WHERE id = ?;

-- name: GetProjectGroup :one
SELECT * FROM project_groups WHERE id = ?;

-- name: SetProjectGroup :one
UPDATE projects SET group_id = ? WHERE id = ? RETURNING *;

-- name: ListAccessibleProjectGroups :many
-- Numbered parameters retain the named bindings in HAVING, which sqlc does not rewrite.
SELECT g.id, g.name, COUNT(p.id) AS project_count
FROM project_groups g LEFT JOIN projects p ON p.group_id = g.id
AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR p.id IN (
    SELECT pm.project_id FROM project_members pm
    WHERE pm.user_id = sqlc.arg(user_id)))
AND (CAST(sqlc.arg(token_id) AS TEXT) = '' OR p.id IN (
    SELECT tp.project_id FROM api_token_projects tp
    WHERE tp.token_id = sqlc.arg(token_id)))
GROUP BY g.id, g.name
HAVING COUNT(p.id) > 0 OR
    (CAST(?1 AS INTEGER) = 1 AND CAST(?3 AS TEXT) = '')
ORDER BY LOWER(g.name) ASC, g.id ASC;

-- name: ListProjectCatalog :many
-- The fifth binding also selects the CASE order because sqlc does not rewrite CASE arguments.
WITH successful_deployments AS (
    SELECT d.release_id, d.environment_id, deployed.project_id,
        ROW_NUMBER() OVER (PARTITION BY deployed.project_id, d.environment_id
            ORDER BY d.created_at DESC, d.id DESC) AS deployment_rank
    FROM deployments d JOIN releases deployed ON deployed.id = d.release_id
    WHERE d.kind = 'deployment' AND d.status = 'succeeded'
    AND COALESCE(d.ansible_check_mode, 0) = 0
    AND NOT EXISTS (SELECT 1 FROM deployment_steps s
        WHERE d.ansible_check_mode IS NULL AND s.deployment_id = d.id
        AND s.kind = 'ansible' AND s.ansible_json LIKE '%"check":true%')
)
SELECT sqlc.embed(p), CAST(COUNT(*) OVER () AS BIGINT) AS total FROM projects p
WHERE (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR p.id IN (
    SELECT pm.project_id FROM project_members pm
    WHERE pm.user_id = sqlc.arg(user_id)))
AND (CAST(sqlc.arg(token_id) AS TEXT) = '' OR p.id IN (
    SELECT tp.project_id FROM api_token_projects tp
    WHERE tp.token_id = sqlc.arg(token_id)))
AND (CAST(sqlc.arg(group_filter) AS BIGINT) = -1
    OR (CAST(sqlc.arg(group_filter) AS BIGINT) = 0 AND p.group_id IS NULL)
    OR p.group_id = sqlc.arg(group_filter))
AND CAST(sqlc.arg(sort_order) AS TEXT) != ''
AND ((LOWER(p.name) LIKE LOWER(sqlc.arg(search)) ESCAPE '!')
    OR (LOWER(COALESCE(p.description, '')) LIKE LOWER(sqlc.arg(search)) ESCAPE '!'))
AND (CAST(sqlc.arg(updates_available) AS INTEGER) = 0 OR EXISTS (
    SELECT 1 FROM releases r CROSS JOIN environments e
    WHERE r.project_id = p.id AND r.kind = 'deployment'
    AND NOT EXISTS (SELECT 1 FROM releases newer
        WHERE newer.project_id = p.id AND newer.kind = 'deployment'
        AND (newer.created_at > r.created_at
            OR (newer.created_at = r.created_at AND newer.id > r.id)))
    AND (p.lifecycle_id IS NULL OR EXISTS (
        SELECT 1 FROM lifecycle_stages ls
        WHERE ls.lifecycle_id = p.lifecycle_id AND ls.environment_id = e.id))
    AND r.id != COALESCE((
        SELECT d.release_id FROM successful_deployments d
        WHERE d.project_id = p.id AND d.environment_id = e.id
        AND d.deployment_rank = 1
    ), 0)
))
ORDER BY
    CASE WHEN CAST(?5 AS TEXT) = 'name' THEN LOWER(p.name) END ASC,
    CASE WHEN CAST(?5 AS TEXT) = 'name_desc' THEN LOWER(p.name) END DESC,
    p.created_at DESC, p.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
