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
SELECT p.* FROM projects p
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
ORDER BY
    CASE WHEN CAST(?5 AS TEXT) = 'name' THEN LOWER(p.name) END ASC,
    CASE WHEN CAST(?5 AS TEXT) = 'name_desc' THEN LOWER(p.name) END DESC,
    p.created_at DESC, p.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountProjectCatalog :one
SELECT COUNT(*) FROM projects p
WHERE (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR p.id IN (
    SELECT pm.project_id FROM project_members pm
    WHERE pm.user_id = sqlc.arg(user_id)))
AND (CAST(sqlc.arg(token_id) AS TEXT) = '' OR p.id IN (
    SELECT tp.project_id FROM api_token_projects tp
    WHERE tp.token_id = sqlc.arg(token_id)))
AND (CAST(sqlc.arg(group_filter) AS BIGINT) = -1
    OR (CAST(sqlc.arg(group_filter) AS BIGINT) = 0 AND p.group_id IS NULL)
    OR p.group_id = sqlc.arg(group_filter))
AND ((LOWER(p.name) LIKE LOWER(sqlc.arg(search)) ESCAPE '!')
    OR (LOWER(COALESCE(p.description, '')) LIKE LOWER(sqlc.arg(search)) ESCAPE '!'));
