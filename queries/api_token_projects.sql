-- name: SetApiTokenScope :execrows
UPDATE api_tokens SET scope = ?, token_role = ?
WHERE id = ? AND revoked_at IS NULL;

-- name: AddApiTokenProject :exec
INSERT INTO api_token_projects (token_id, project_id) VALUES (?, ?);

-- name: DeleteApiTokenProjects :exec
DELETE FROM api_token_projects WHERE token_id = ?;

-- name: ListApiTokenProjectIDs :many
SELECT project_id FROM api_token_projects WHERE token_id = ? ORDER BY project_id;

-- name: ListTokenProjectsPaginated :many
SELECT p.* FROM projects p JOIN api_token_projects tp ON tp.project_id = p.id
WHERE tp.token_id = sqlc.arg(token_id)
AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (
    SELECT 1 FROM project_members pm
    WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)))
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountTokenProjects :one
SELECT COUNT(*) FROM projects p JOIN api_token_projects tp ON tp.project_id = p.id
WHERE tp.token_id = sqlc.arg(token_id)
AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (
    SELECT 1 FROM project_members pm
    WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)));
