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
