-- name: CreateUser :one
INSERT INTO users (email, password_hash, name, role) VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;
