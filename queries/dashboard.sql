-- name: CountDashboardProjects :one
SELECT COUNT(*) FROM projects p
WHERE CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id));

-- name: CountDashboardDeploymentsToday :one
SELECT COUNT(*) FROM deployments d JOIN releases r ON r.id = d.release_id
WHERE d.kind = 'deployment' AND d.created_at >= strftime('%s','now','start of day')
AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = r.project_id AND pm.user_id = sqlc.arg(user_id)));

-- name: ListDashboardDeployments :many
SELECT d.id, d.release_id, d.environment_id, d.status,
 d.started_at, d.finished_at, d.created_at, d.forced, d.note, d.ansible_check_mode,
 p.name AS project_name, r.version AS release_version, e.name AS environment_name
FROM deployments d JOIN releases r ON d.release_id = r.id
JOIN projects p ON r.project_id = p.id JOIN environments e ON d.environment_id = e.id
WHERE d.kind = 'deployment'
AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)))
AND (sqlc.arg(section) = 'recent'
 OR (sqlc.arg(section) = 'active' AND d.status IN ('queued', 'pending', 'running', 'publishing_artifact', 'pending_approval', 'awaiting_artifact_approval'))
 OR (sqlc.arg(section) = 'latest' AND COALESCE(d.ansible_check_mode, 0) = 0
     AND NOT EXISTS (SELECT 1 FROM deployment_steps s WHERE d.ansible_check_mode IS NULL AND s.deployment_id = d.id AND s.kind = 'ansible' AND s.ansible_json LIKE '%"check":true%')
     AND NOT EXISTS (SELECT 1 FROM deployments newer WHERE newer.release_id = d.release_id AND newer.environment_id = d.environment_id AND newer.kind = 'deployment' AND COALESCE(newer.ansible_check_mode, 0) = 0
         AND NOT EXISTS (SELECT 1 FROM deployment_steps s WHERE newer.ansible_check_mode IS NULL AND s.deployment_id = newer.id AND s.kind = 'ansible' AND s.ansible_json LIKE '%"check":true%')
         AND (newer.created_at > d.created_at OR (newer.created_at = d.created_at AND newer.id > d.id)))))
ORDER BY d.created_at DESC, d.id DESC LIMIT sqlc.arg(row_limit);
