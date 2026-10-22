-- name: GetIdempotency :one
SELECT request_hash, response_json FROM api_idempotency
WHERE user_id = ? AND project_id = ? AND operation = ? AND key_hash = ?;

-- name: ClaimIdempotency :exec
INSERT INTO api_idempotency (user_id, project_id, operation, key_hash, request_hash)
VALUES (?, ?, ?, ?, ?);

-- name: CompleteIdempotency :exec
UPDATE api_idempotency SET response_json = ?
WHERE user_id = ? AND project_id = ? AND operation = ? AND key_hash = ?;
