-- name: SetStepExecutionTarget :execrows
UPDATE steps SET execution_target = ? WHERE id = ?;

-- name: AddStepAgentSelector :exec
INSERT INTO step_agent_selectors (step_id, label) VALUES (?, ?);

-- name: DeleteStepAgentSelectors :exec
DELETE FROM step_agent_selectors WHERE step_id = ?;

-- name: ListStepAgentSelectors :many
SELECT label FROM step_agent_selectors WHERE step_id = ? ORDER BY label;

-- name: ListStepAgentSelectorsByStepIDs :many
SELECT step_id, label FROM step_agent_selectors
WHERE step_id IN (sqlc.slice('step_ids')) ORDER BY step_id ASC, label ASC;

-- name: SetTemplateExecutionTarget :execrows
UPDATE step_templates SET execution_target = ? WHERE id = ?;

-- name: AddTemplateAgentSelector :exec
INSERT INTO step_template_agent_selectors (template_id, label) VALUES (?, ?);

-- name: DeleteTemplateAgentSelectors :exec
DELETE FROM step_template_agent_selectors WHERE template_id = ?;

-- name: ListTemplateAgentSelectors :many
SELECT label FROM step_template_agent_selectors WHERE template_id = ? ORDER BY label;

-- name: ListTemplateAgentSelectorsByTemplateIDs :many
SELECT template_id, label FROM step_template_agent_selectors
WHERE template_id IN (sqlc.slice('template_ids'))
ORDER BY template_id ASC, label ASC;

-- name: AddTemplateVersionAgentSelector :exec
INSERT INTO step_template_version_agent_selectors (template_version_id, label) VALUES (?, ?);

-- name: SetTemplateVersionExecutionTarget :execrows
UPDATE step_template_versions SET execution_target = ? WHERE id = ?;

-- name: ListTemplateVersionAgentSelectors :many
SELECT label FROM step_template_version_agent_selectors WHERE template_version_id = ? ORDER BY label;

-- name: ListTemplateVersionAgentSelectorsByVersionIDs :many
SELECT template_version_id, label FROM step_template_version_agent_selectors
WHERE template_version_id IN (sqlc.slice('version_ids'))
ORDER BY template_version_id ASC, label ASC;

-- name: DeleteTemplateVersionAgentSelectors :exec
DELETE FROM step_template_version_agent_selectors
WHERE template_version_id IN (
    SELECT id FROM step_template_versions WHERE template_id = ?
);
