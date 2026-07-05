-- name: ListStepTemplates :many
SELECT * FROM step_templates ORDER BY name ASC;

-- name: GetStepTemplate :one
SELECT * FROM step_templates WHERE id = ?;

-- name: CreateStepTemplate :one
INSERT INTO step_templates (name, script_body) VALUES (?, ?) RETURNING *;

-- name: UpdateStepTemplate :one
UPDATE step_templates SET name = ?, script_body = ? WHERE id = ? RETURNING *;

-- name: DeleteStepTemplate :exec
DELETE FROM step_templates WHERE id = ?;

-- name: CreateStepTemplateVersion :one
INSERT INTO step_template_versions (template_id, version_number, name, script_body) VALUES (?, ?, ?, ?) RETURNING *;

-- name: ListStepTemplateVersions :many
SELECT * FROM step_template_versions WHERE template_id = ? ORDER BY version_number DESC;

-- name: GetLatestStepTemplateVersionNumber :one
SELECT COALESCE(MAX(version_number), 0) FROM step_template_versions WHERE template_id = ?;

-- name: GetStepTemplateVersion :one
SELECT * FROM step_template_versions WHERE id = ?;
