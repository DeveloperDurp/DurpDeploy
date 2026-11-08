-- name: CountDashboardProjects :one
SELECT COUNT(*) FROM projects p
WHERE CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id));

-- name: CountDashboardDeploymentsToday :one
SELECT COUNT(*) FROM deployments d JOIN releases r ON r.id = d.release_id
WHERE d.kind = 'deployment' AND d.created_at >= strftime('%s','now','start of day')
AND (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = r.project_id AND pm.user_id = sqlc.arg(user_id)));

-- Dashboard rows come in two sections:
--
--  attention: rows that need a human decision or an unresolved failure.
--    pending_approval / awaiting_artifact_approval / cleanup_unconfirmed
--    always appear. failed and expired rows appear only while no later real
--    deployment for the same project + environment has taken over:
--    a newer succeeded deployment resolves them, a newer failed or expired
--    deployment collapses them, and a newer in-flight deployment (pending,
--    queued, running, publishing_artifact, pending_approval,
--    awaiting_artifact_approval) means the retry is already handling it.
--    Only cancelled and rejected later deployments leave the failure
--    visible: a dead end neither resolves nor takes over. One failure row
--    per project + environment. Rejected rows never appear as attention:
--    a rejection is a deliberate human decision, so it is not attention.
--    Runbook executions stay off the dashboard: their approve flow works,
--    but re-run and history live on the runbook pages.
--
--  active: in-flight deployments. queued, pending, publishing_artifact and
--    running. The system is working on these; no human decision is owed.
--
-- "Real" deployments exclude ansible check-mode runs, mirroring the
-- historical latest-per-release rule: a failed dry run is history, not an
-- environment state.

-- name: ListDashboardDeployments :many
SELECT d.id, d.release_id, d.environment_id, d.status,
 d.started_at, d.finished_at, d.created_at, d.forced, d.note, d.ansible_check_mode,
 p.name AS project_name, r.version AS release_version, e.name AS environment_name
FROM deployments d JOIN releases r ON d.release_id = r.id
JOIN projects p ON r.project_id = p.id JOIN environments e ON d.environment_id = e.id
WHERE (CAST(sqlc.arg(is_admin) AS INTEGER) = 1 OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)))
AND (
 (sqlc.arg(section) = 'attention' AND d.kind = 'deployment' AND (
    d.status IN ('pending_approval', 'awaiting_artifact_approval', 'cleanup_unconfirmed')
    OR (d.status IN ('failed', 'expired')
        AND COALESCE(d.ansible_check_mode, 0) = 0
        AND NOT EXISTS (SELECT 1 FROM deployment_steps s WHERE d.ansible_check_mode IS NULL AND s.deployment_id = d.id AND s.kind = 'ansible' AND s.ansible_json LIKE '%"check":true%')
        AND NOT EXISTS (
            SELECT 1 FROM deployments newer JOIN releases rnew ON rnew.id = newer.release_id
            WHERE rnew.project_id = r.project_id
             AND newer.environment_id = d.environment_id
             AND newer.kind = 'deployment'
             AND newer.status NOT IN ('cancelled', 'rejected')
             AND COALESCE(newer.ansible_check_mode, 0) = 0
             AND NOT EXISTS (SELECT 1 FROM deployment_steps s WHERE newer.ansible_check_mode IS NULL AND s.deployment_id = newer.id AND s.kind = 'ansible' AND s.ansible_json LIKE '%"check":true%')
             AND (newer.created_at > d.created_at OR (newer.created_at = d.created_at AND newer.id > d.id)))
    ))
 )
 OR (sqlc.arg(section) = 'active' AND d.kind = 'deployment' AND d.status IN ('queued', 'pending', 'running', 'publishing_artifact'))
)
ORDER BY d.created_at DESC, d.id DESC LIMIT sqlc.arg(row_limit);
