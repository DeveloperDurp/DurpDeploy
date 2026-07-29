-- name: CreateApiToken :one
INSERT INTO api_tokens (id, user_id, name, token_prefix, token_hash, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetApiTokenByHash :one
SELECT t.id, t.user_id, t.name, t.token_prefix, t.token_hash, t.scope, t.last_used_at, t.expires_at, t.created_at, t.revoked_at, u.email, u.name AS user_name, u.role FROM api_tokens t JOIN users u ON t.user_id = u.id WHERE t.token_hash = ? AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > ?);

-- name: ListApiTokensByUser :many
SELECT id, token_prefix, name, scope, last_used_at, expires_at, created_at, revoked_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC;

-- name: ListApiTokensByUserPaginated :many
SELECT id, token_prefix, name, scope, last_used_at, expires_at, created_at, revoked_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountApiTokensByUser :one
SELECT COUNT(*) FROM api_tokens WHERE user_id = ?;

-- name: ListAllApiTokens :many
SELECT t.id, t.token_prefix, t.name, t.user_id, u.email, u.name AS user_name, t.scope, t.last_used_at, t.expires_at, t.created_at, t.revoked_at FROM api_tokens t JOIN users u ON t.user_id = u.id ORDER BY t.created_at DESC;

-- name: ListAllApiTokensPaginated :many
SELECT t.id, t.token_prefix, t.name, t.user_id, u.email, u.name AS user_name, t.scope, t.last_used_at, t.expires_at, t.created_at, t.revoked_at FROM api_tokens t JOIN users u ON t.user_id = u.id ORDER BY t.created_at DESC
LIMIT ? OFFSET ?;

-- name: CountAllApiTokens :one
SELECT COUNT(*) FROM api_tokens;

-- name: ListAllApiTokensByUser :many
SELECT t.id, t.token_prefix, t.name, t.user_id, u.email, u.name AS user_name, t.scope, t.last_used_at, t.expires_at, t.created_at, t.revoked_at FROM api_tokens t JOIN users u ON t.user_id = u.id WHERE t.user_id = ? ORDER BY t.created_at DESC;

-- name: ListAllApiTokensByUserPaginated :many
SELECT t.id, t.token_prefix, t.name, t.user_id, u.email, u.name AS user_name, t.scope, t.last_used_at, t.expires_at, t.created_at, t.revoked_at FROM api_tokens t JOIN users u ON t.user_id = u.id WHERE t.user_id = ? ORDER BY t.created_at DESC
LIMIT ? OFFSET ?;

-- name: CountAllApiTokensByUser :one
SELECT COUNT(*) FROM api_tokens WHERE user_id = ?;

-- name: GetApiTokenByID :one
SELECT id, user_id, name, token_prefix, token_hash, scope, last_used_at, expires_at, created_at, revoked_at FROM api_tokens WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeApiToken :exec
UPDATE api_tokens SET revoked_at = strftime('%s','now') WHERE id = ? AND revoked_at IS NULL;

-- name: TouchApiTokenLastUsed :exec
UPDATE api_tokens SET last_used_at = ? WHERE id = ?;
