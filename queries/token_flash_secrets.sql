-- name: CreateTokenFlashSecret :one
INSERT INTO token_flash_secrets (id, user_id, session_id, token_value, token_name, expires_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: ConsumeTokenFlashSecret :one
DELETE FROM token_flash_secrets WHERE id = ? AND user_id = ? AND session_id = ? AND expires_at > ? RETURNING token_value, token_name;

-- name: DeleteExpiredTokenFlashSecrets :exec
DELETE FROM token_flash_secrets WHERE expires_at <= ?;
