-- name: ListTokenLifecycles :many
SELECT lc.* FROM lifecycles lc WHERE EXISTS (
    SELECT 1 FROM projects p
    JOIN api_token_projects tp ON tp.project_id = p.id
    WHERE tp.token_id = sqlc.arg(token_id) AND p.lifecycle_id = lc.id
    AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (
        SELECT 1 FROM project_members pm
        WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)))
) ORDER BY lc.created_at DESC, lc.id DESC;

-- name: ListTokenEnvironments :many
SELECT e.* FROM environments e WHERE EXISTS (
    SELECT 1 FROM projects p
    JOIN api_token_projects tp ON tp.project_id = p.id
    WHERE tp.token_id = sqlc.arg(token_id)
    AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (
        SELECT 1 FROM project_members pm
        WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)))
    AND (p.lifecycle_id IS NULL OR EXISTS (
        SELECT 1 FROM lifecycle_stages ls
        WHERE ls.lifecycle_id = p.lifecycle_id AND ls.environment_id = e.id
    ) OR EXISTS (
        SELECT 1 FROM deployments d JOIN releases r ON r.id = d.release_id
        WHERE r.project_id = p.id AND d.environment_id = e.id
    ) OR EXISTS (
        SELECT 1 FROM runbook_executions re JOIN deployments d ON d.id = re.deployment_id
        JOIN runbook_versions rv ON rv.id = re.runbook_version_id
        JOIN runbooks rb ON rb.id = rv.runbook_id
        WHERE rb.project_id = p.id AND d.environment_id = e.id
    ))
) ORDER BY e.created_at DESC, e.id DESC;
