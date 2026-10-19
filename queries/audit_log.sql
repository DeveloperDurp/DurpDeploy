-- name: CreateAuditLog :one
INSERT INTO audit_log (user_id, action, entity_type, entity_id, details) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: ListAuditLogs :many
-- ponytail: id DESC tiebreaker. created_at is unixepoch() (seconds),
-- so rows inserted in the same second tie. Without id DESC the index
-- returns the older row first, which breaks newest-first callers.
SELECT * FROM audit_log ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListAuditLogsFiltered :many
SELECT * FROM audit_log
WHERE (CAST(sqlc.narg(f_user_id) AS INTEGER) IS NULL OR user_id = CAST(sqlc.narg(f_user_id) AS INTEGER))
  AND (CAST(sqlc.narg(f_action) AS TEXT) IS NULL OR action = CAST(sqlc.narg(f_action) AS TEXT))
  AND (CAST(sqlc.narg(f_entity_type) AS TEXT) IS NULL OR entity_type = CAST(sqlc.narg(f_entity_type) AS TEXT))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: CountAuditLogs :one
SELECT COUNT(*) FROM audit_log;

-- name: ListAuditLogEntries :many
-- Release and deployment kinds are constrained to deployment or runbook.
SELECT sqlc.embed(audit_log), COALESCE(actor.name, '') AS actor_name,
    COALESCE(actor.email, '') AS actor_email,
    COALESCE(target_user.id, project.id, environment.id, lifecycle.id,
        template.id, deployment.id, release.id, runbook.id, 0) AS target_id,
    COALESCE(release.project_id, runbook.project_id, 0) AS target_project_id
FROM audit_log
LEFT JOIN users actor ON actor.id = audit_log.user_id
LEFT JOIN users target_user ON audit_log.entity_type = 'user' AND target_user.id = audit_log.entity_id
LEFT JOIN projects project ON audit_log.entity_type = 'project' AND project.id = audit_log.entity_id
LEFT JOIN environments environment ON audit_log.entity_type = 'environment' AND environment.id = audit_log.entity_id
LEFT JOIN lifecycles lifecycle ON audit_log.entity_type = 'lifecycle' AND lifecycle.id = audit_log.entity_id
LEFT JOIN step_templates template ON audit_log.entity_type = 'template' AND template.id = audit_log.entity_id
LEFT JOIN deployments deployment ON audit_log.entity_type = 'deployment' AND deployment.id = audit_log.entity_id AND deployment.kind = audit_log.entity_type
LEFT JOIN releases release ON audit_log.entity_type = 'release' AND release.id = audit_log.entity_id AND release.kind <> 'runbook'
LEFT JOIN runbooks runbook ON audit_log.entity_type = 'runbook' AND runbook.id = audit_log.entity_id AND runbook.deleted_at IS NULL
WHERE (CAST(sqlc.narg(f_user_id) AS INTEGER) IS NULL OR audit_log.user_id = CAST(sqlc.narg(f_user_id) AS INTEGER))
  AND (CAST(sqlc.narg(f_user) AS TEXT) IS NULL
    OR LOWER(actor.name) = LOWER(CAST(sqlc.narg(f_user) AS TEXT))
    OR LOWER(actor.email) = LOWER(CAST(sqlc.narg(f_user) AS TEXT)))
  AND (CAST(sqlc.narg(f_action) AS TEXT) IS NULL OR audit_log.action = CAST(sqlc.narg(f_action) AS TEXT))
  AND (CAST(sqlc.narg(f_entity_type) AS TEXT) IS NULL OR audit_log.entity_type = CAST(sqlc.narg(f_entity_type) AS TEXT))
ORDER BY audit_log.created_at DESC, audit_log.id DESC
LIMIT sqlc.arg(page_limit);

-- name: PruneAuditLogs :exec
-- ponytail: preserve rows tied to live deployments/releases; entity_type is singular. Add more NOT EXISTS guards as the audit entity set grows.
DELETE FROM audit_log
WHERE audit_log.created_at < ?
  AND NOT EXISTS (SELECT 1 FROM deployments WHERE audit_log.entity_type = 'deployment' AND id = audit_log.entity_id)
  AND NOT EXISTS (SELECT 1 FROM releases WHERE audit_log.entity_type = 'release' AND id = audit_log.entity_id)
