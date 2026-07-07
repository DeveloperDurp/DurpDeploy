-- name: CreateSession :one
INSERT INTO sessions (id, user_id, csrf_token, expires_at, ip_address, user_agent) VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetSession :one
SELECT s.id, s.user_id, s.csrf_token, s.created_at, s.expires_at, s.ip_address, s.user_agent, u.email, u.name, u.role FROM sessions s JOIN users u ON s.user_id = u.id WHERE s.id = ? AND s.expires_at > ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ?;

-- name: TouchSession :exec
UPDATE sessions SET expires_at = ? WHERE id = ?;
