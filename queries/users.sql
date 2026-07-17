-- name: CreateUser :one
INSERT INTO users (email, password_hash, name, role) VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;

-- name: UpdateUser :exec
UPDATE users SET name = ?, role = ?, updated_at = unixepoch() WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = unixepoch() WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: DeleteSessionsByUser :exec
DELETE FROM sessions WHERE user_id = ?;
