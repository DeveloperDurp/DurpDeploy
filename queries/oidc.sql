-- name: GetOIDCIdentity :one
SELECT * FROM oidc_identities WHERE issuer = ? AND subject = ?;

-- name: GetOIDCIdentityByUserID :one
SELECT * FROM oidc_identities WHERE user_id = ?;

-- name: CreateOIDCIdentity :one
INSERT INTO oidc_identities (issuer, subject, user_id)
VALUES (?, ?, ?) RETURNING *;

-- name: CreateOIDCTransaction :exec
INSERT INTO oidc_transactions (state_hash, expires_at) VALUES (?, ?);

-- name: ConsumeOIDCTransaction :execrows
DELETE FROM oidc_transactions WHERE state_hash = ? AND expires_at > ?;

-- name: DeleteExpiredOIDCTransactions :exec
DELETE FROM oidc_transactions WHERE expires_at <= ?;

-- name: UpdateOIDCUser :one
UPDATE users
SET email = sqlc.arg(email),
    name = sqlc.arg(name),
    role = sqlc.arg(role),
    updated_at = unixepoch()
WHERE id = sqlc.arg(id) AND role = sqlc.arg(expected_role)
RETURNING *;
