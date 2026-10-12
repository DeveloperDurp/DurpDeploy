-- name: ListStepTemplates :many
SELECT * FROM step_templates ORDER BY name ASC;

-- name: ListStepTemplatesPaginated :many
SELECT * FROM step_templates ORDER BY name ASC
LIMIT ? OFFSET ?;

-- name: CountStepTemplates :one
SELECT COUNT(*) FROM step_templates;

-- name: GetStepTemplate :one
SELECT * FROM step_templates WHERE id = ?;

-- name: CreateStepTemplate :one
INSERT INTO step_templates (name, script_body, interpreter, agent_execution_mode, container_image, variable_names, network_mode, approval_artifact_path, approval_review_path, approval_review_format)
VALUES (?, ?, COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash'), COALESCE(NULLIF(CAST(sqlc.arg(agent_execution_mode) AS TEXT), ''), 'host'), sqlc.arg(container_image), sqlc.arg(variable_names), sqlc.arg(network_mode), sqlc.arg(approval_artifact_path), sqlc.arg(approval_review_path), sqlc.arg(approval_review_format))
RETURNING *;

-- name: UpdateStepTemplate :one
UPDATE step_templates SET name = ?, script_body = ?,
interpreter = COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash'),
agent_execution_mode = COALESCE(NULLIF(CAST(sqlc.arg(agent_execution_mode) AS TEXT), ''), 'host'),
container_image = sqlc.arg(container_image), variable_names = sqlc.arg(variable_names), network_mode = sqlc.arg(network_mode), approval_artifact_path = sqlc.arg(approval_artifact_path), approval_review_path = sqlc.arg(approval_review_path), approval_review_format = sqlc.arg(approval_review_format)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: DeleteStepTemplate :exec
DELETE FROM step_templates WHERE id = ?;

-- name: CreateStepTemplateVersion :one
INSERT INTO step_template_versions (template_id, version_number, name, script_body, interpreter, agent_execution_mode, container_image, variable_names, network_mode, approval_artifact_path, approval_review_path, approval_review_format)
VALUES (?, ?, ?, ?, COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash'), COALESCE(NULLIF(CAST(sqlc.arg(agent_execution_mode) AS TEXT), ''), 'host'), sqlc.arg(container_image), sqlc.arg(variable_names), sqlc.arg(network_mode), sqlc.arg(approval_artifact_path), sqlc.arg(approval_review_path), sqlc.arg(approval_review_format))
RETURNING *;

-- name: ListStepTemplateVersions :many
SELECT * FROM step_template_versions WHERE template_id = ? ORDER BY version_number DESC;

-- name: GetLatestStepTemplateVersionNumber :one
SELECT CAST(COALESCE(MAX(version_number), 0) AS BIGINT)
FROM step_template_versions WHERE template_id = ?;

-- name: GetStepTemplateVersion :one
SELECT * FROM step_template_versions WHERE id = ?;
