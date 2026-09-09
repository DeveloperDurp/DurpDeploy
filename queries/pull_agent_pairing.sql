-- name: CreateAgentPairing :one
INSERT INTO agent_pairings (agent_id, pairing_code_hash, agent_public_identity, agent_pin, expires_at)
VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: GetAgentPairing :one
SELECT * FROM agent_pairings WHERE agent_id = ?;

-- name: ListAgentPairingRecoveryCandidates :many
SELECT p.*, a.endpoint
FROM agent_pairings p
JOIN agents a ON a.id = p.agent_id
WHERE p.pairing_code_hash = sqlc.arg(pairing_code_hash)
   OR p.agent_pin = sqlc.arg(agent_pin)
   OR a.endpoint = sqlc.arg(endpoint)
ORDER BY p.agent_id;

-- name: CreateCommittingAgentPairing :one
INSERT INTO agent_pairings (
    agent_id, pairing_code_hash, agent_public_identity, agent_pin,
    server_public_identity, server_pin, encrypted_identity, state,
    expires_at, updated_at, server_pull_endpoint
)
VALUES (
    sqlc.arg(agent_id), sqlc.arg(pairing_code_hash),
    sqlc.arg(agent_public_identity), sqlc.arg(agent_pin),
    sqlc.arg(server_public_identity), sqlc.arg(server_pin),
    sqlc.arg(encrypted_identity), 'committing', sqlc.arg(expires_at),
    sqlc.arg(now), sqlc.arg(server_pull_endpoint)
)
RETURNING *;

-- name: BeginPairingCommit :execrows
UPDATE agent_pairings SET state = 'committing', server_public_identity = sqlc.arg(server_public_identity),
    server_pin = sqlc.arg(server_pin), encrypted_identity = sqlc.arg(encrypted_identity), updated_at = sqlc.arg(now)
WHERE agent_id = sqlc.arg(agent_id) AND pairing_code_hash = sqlc.arg(pairing_code_hash)
  AND state = 'pending' AND expires_at > sqlc.arg(now)
  AND EXISTS (SELECT 1 FROM agents WHERE id = agent_pairings.agent_id AND status = 'pending');

-- name: CompleteAgentPairing :execrows
UPDATE agent_pairings SET state = 'paired', paired_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE agent_id = sqlc.arg(agent_id) AND state = 'committing'
  AND server_pin = sqlc.arg(server_pin) AND encrypted_identity IS NOT NULL;

-- name: ExpireCommittingAgentPairing :execrows
UPDATE agent_pairings SET state = 'expired', updated_at = sqlc.arg(now)
WHERE agent_id = sqlc.arg(agent_id) AND state = 'committing';

-- name: DeleteExpiredAgentPairing :execrows
DELETE FROM agent_pairings
WHERE agent_id = sqlc.arg(agent_id) AND state = 'expired';

-- name: ActivatePairedAgent :execrows
UPDATE agents SET status = 'active', certificate_pem = sqlc.arg(certificate_pem),
    certificate_fingerprint = sqlc.arg(certificate_fingerprint), encrypted_identity = sqlc.arg(encrypted_identity),
    updated_at = unixepoch()
WHERE id = sqlc.arg(id) AND status = 'pending'
  AND EXISTS (SELECT 1 FROM agent_pairings WHERE agent_id = agents.id AND state = 'paired');

-- name: ExpireAgentPairings :execrows
UPDATE agent_pairings SET state = 'expired', updated_at = sqlc.arg(now)
WHERE state = 'pending' AND expires_at <= sqlc.arg(now);
