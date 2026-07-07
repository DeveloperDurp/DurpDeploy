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