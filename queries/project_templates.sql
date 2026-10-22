-- name: CreateProjectTemplate :one
INSERT INTO project_templates (source_project_id, created_by, name, description, snapshot_json)
VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: GetProjectTemplate :one
SELECT * FROM project_templates WHERE source_project_id = ? AND id = ?;

-- name: ListProjectTemplateRotationKeys :many
SELECT id FROM project_templates ORDER BY id ASC;

-- name: GetProjectTemplateRotationValue :one
SELECT snapshot_json FROM project_templates WHERE id = ?;

-- name: UpdateProjectTemplateRotationValue :exec
UPDATE project_templates SET snapshot_json = ? WHERE id = ?;

-- name: ListAccessibleProjectTemplates :many
SELECT t.id, t.source_project_id, t.created_by, t.name, t.description, t.created_at,
    p.name AS source_project_name
FROM project_templates t JOIN projects p ON p.id = t.source_project_id
WHERE (CAST(sqlc.arg(source_project_id) AS BIGINT) = 0 OR t.source_project_id = sqlc.arg(source_project_id))
    AND (CAST(sqlc.arg(is_admin) AS BIGINT) = 1 OR EXISTS (
        SELECT 1 FROM project_members m
        WHERE m.project_id = t.source_project_id AND m.user_id = sqlc.arg(user_id)))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAccessibleProjectTemplates :one
SELECT COUNT(*) FROM project_templates t
WHERE (CAST(sqlc.arg(source_project_id) AS BIGINT) = 0 OR t.source_project_id = sqlc.arg(source_project_id))
    AND (CAST(sqlc.arg(is_admin) AS BIGINT) = 1 OR EXISTS (
        SELECT 1 FROM project_members m
        WHERE m.project_id = t.source_project_id AND m.user_id = sqlc.arg(user_id)));
