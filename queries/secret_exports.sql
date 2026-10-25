-- name: CreateSecretExport :one
INSERT INTO secret_exports (id, user_id, session_id, token_id, claim_hash, scope, scope_id, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetSecretExport :one
SELECT * FROM secret_exports WHERE id = ? AND user_id = ? AND expires_at > ?;

-- name: BindSecretExport :execrows
UPDATE secret_exports SET session_id = ?
WHERE id = ? AND user_id = ? AND session_id = '' AND approved = 0 AND expires_at > ?;

-- name: SetSecretExportAuth :execrows
UPDATE secret_exports SET auth_hash = ?
WHERE id = ? AND user_id = ? AND session_id = ? AND approved = 0 AND expires_at > ?;

-- name: SecretExportForAuth :one
SELECT id FROM secret_exports
WHERE user_id = ? AND session_id = ? AND auth_hash = ? AND approved = 0 AND expires_at > ?;

-- name: ApproveSecretExport :execrows
UPDATE secret_exports SET approved = 1
WHERE secret_exports.id = ? AND secret_exports.user_id = ? AND secret_exports.session_id = ? AND approved = 0 AND secret_exports.expires_at > ?
AND EXISTS (SELECT 1 FROM sessions JOIN users ON users.id = sessions.user_id
    WHERE sessions.id = secret_exports.session_id AND sessions.user_id = secret_exports.user_id
    AND sessions.expires_at > sqlc.arg(now) AND users.role = 'admin');

-- name: ConsumeSecretExport :execrows
DELETE FROM secret_exports
WHERE secret_exports.id = ? AND secret_exports.user_id = ? AND secret_exports.session_id = ? AND approved = 1 AND secret_exports.expires_at > ?
AND EXISTS (SELECT 1 FROM sessions JOIN users ON users.id = sessions.user_id
    WHERE sessions.id = secret_exports.session_id AND sessions.user_id = secret_exports.user_id
    AND sessions.expires_at > sqlc.arg(now) AND users.role = 'admin');

-- name: CancelSecretExport :exec
DELETE FROM secret_exports WHERE id = ? AND user_id = ?;

-- name: DeleteExpiredSecretExports :exec
DELETE FROM secret_exports WHERE expires_at <= ?;
