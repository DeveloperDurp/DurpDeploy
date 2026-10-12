-- name: ListTOTPRotationKeys :many
SELECT user_id FROM mfa_totp ORDER BY user_id;

-- name: GetTOTPRotationValue :one
SELECT encrypted_seed FROM mfa_totp WHERE user_id = ?;

-- name: UpdateTOTPRotationValue :exec
UPDATE mfa_totp SET encrypted_seed = ? WHERE user_id = ?;

-- name: ListTOTPEnrollmentRotationKeys :many
SELECT token_hash FROM mfa_challenges WHERE purpose = 'totp_enroll' ORDER BY token_hash;

-- name: GetTOTPEnrollmentRotationValue :one
SELECT ceremony_json FROM mfa_challenges WHERE token_hash = ?;

-- name: UpdateTOTPEnrollmentRotationValue :exec
UPDATE mfa_challenges SET ceremony_json = ? WHERE token_hash = ?;

-- name: ListAgentIdentityRotationKeys :many
SELECT id FROM agents ORDER BY id;

-- name: GetAgentIdentityRotationValue :one
SELECT encrypted_identity FROM agents WHERE id = ?;

-- name: UpdateAgentIdentityRotationValue :exec
UPDATE agents SET encrypted_identity = ? WHERE id = ?;

-- name: ListPairingIdentityRotationKeys :many
SELECT agent_id FROM agent_pairings ORDER BY agent_id;

-- name: GetPairingIdentityRotationValue :one
SELECT encrypted_identity FROM agent_pairings WHERE agent_id = ?;

-- name: UpdatePairingIdentityRotationValue :exec
UPDATE agent_pairings SET encrypted_identity = ? WHERE agent_id = ?;

-- name: ListRemoteClaimLogBufferRotationKeys :many
SELECT deployment_id FROM remote_deployment_claims ORDER BY deployment_id;

-- name: GetRemoteClaimLogBufferRotationValue :one
SELECT log_buffer_ciphertext FROM remote_deployment_claims WHERE deployment_id = ?;

-- name: UpdateRemoteClaimLogBufferRotationValue :exec
UPDATE remote_deployment_claims SET log_buffer_ciphertext = ? WHERE deployment_id = ?;

-- name: ListRemoteStepLogBufferRotationKeys :many
SELECT deployment_id, step_index, agent_id FROM remote_step_runs
ORDER BY deployment_id, step_index, agent_id;

-- name: GetRemoteStepLogBufferRotationValue :one
SELECT log_buffer_ciphertext FROM remote_step_runs
WHERE deployment_id = ? AND step_index = ? AND agent_id = ?;

-- name: UpdateRemoteStepLogBufferRotationValue :exec
UPDATE remote_step_runs SET log_buffer_ciphertext = ?
WHERE deployment_id = ? AND step_index = ? AND agent_id = ?;
