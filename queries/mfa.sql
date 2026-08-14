-- name: CreateTOTP :one
INSERT INTO mfa_totp (user_id, encrypted_seed, last_accepted_step)
VALUES (?, ?, ?) RETURNING *;

-- name: GetTOTPByUserID :one
SELECT * FROM mfa_totp WHERE user_id = ?;

-- name: UpdateTOTP :one
UPDATE mfa_totp
SET encrypted_seed = ?, last_accepted_step = ?, updated_at = unixepoch()
WHERE user_id = ? RETURNING *;

-- name: AdvanceTOTPIfNewer :execrows
UPDATE mfa_totp
SET last_accepted_step = sqlc.arg(next_step), updated_at = unixepoch()
WHERE user_id = sqlc.arg(user_id)
  AND (last_accepted_step IS NULL OR last_accepted_step < sqlc.arg(next_step));

-- name: DeleteTOTP :execrows
DELETE FROM mfa_totp WHERE user_id = ?;

-- name: CreateWebAuthnUser :one
INSERT INTO webauthn_users (user_id, rp_id, user_handle)
VALUES (?, ?, ?) RETURNING *;

-- name: GetWebAuthnUserByUserID :one
SELECT * FROM webauthn_users WHERE user_id = ?;

-- name: CreateWebAuthnCredential :one
INSERT INTO webauthn_credentials (
    credential_id, user_id, name, public_key, aaguid, transports_json, flags,
    sign_count, clone_warning, attachment, attestation_type, attestation_format,
    attestation_client_data_json, attestation_client_data_hash,
    attestation_authenticator_data, attestation_public_key_algorithm,
    attestation_object
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetWebAuthnCredentialByID :one
SELECT * FROM webauthn_credentials WHERE credential_id = ?;

-- name: ListWebAuthnCredentialsByUserID :many
SELECT * FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at, name;

-- name: UpdateWebAuthnCredentialCounter :one
UPDATE webauthn_credentials
SET sign_count = ?, clone_warning = ?, updated_at = unixepoch()
WHERE credential_id = ? RETURNING *;

-- name: DeleteWebAuthnCredential :exec
DELETE FROM webauthn_credentials WHERE credential_id = ?;

-- name: DeleteWebAuthnCredentialByUserID :execrows
DELETE FROM webauthn_credentials WHERE credential_id = ? AND user_id = ?;

-- name: RenameWebAuthnCredentialByUserID :execrows
UPDATE webauthn_credentials
SET name = ?, updated_at = unixepoch()
WHERE credential_id = ? AND user_id = ?;

-- name: DeleteWebAuthnCredentialsByUserID :exec
DELETE FROM webauthn_credentials WHERE user_id = ?;

-- name: CountMFAFactors :one
SELECT
    (SELECT COUNT(*) FROM mfa_totp AS totp WHERE totp.user_id = sqlc.arg(target_user_id)) +
    (SELECT COUNT(*) FROM webauthn_credentials AS credential WHERE credential.user_id = sqlc.arg(target_user_id))
    AS factor_count;

-- name: LockMFAUser :execrows
UPDATE users SET updated_at = updated_at WHERE id = ?;

-- name: CreateRecoveryCode :one
INSERT INTO mfa_recovery_codes (id, user_id, code_hash)
VALUES (?, ?, ?) RETURNING *;

-- name: ConsumeRecoveryCode :one
UPDATE mfa_recovery_codes SET used_at = ?
WHERE user_id = ? AND code_hash = ? AND used_at IS NULL RETURNING *;

-- name: ListUnusedRecoveryCodesByUserID :many
SELECT * FROM mfa_recovery_codes
WHERE user_id = ? AND used_at IS NULL ORDER BY created_at, id;

-- name: DeleteRecoveryCodesByUserID :exec
DELETE FROM mfa_recovery_codes WHERE user_id = ?;

-- name: CreateMFAChallenge :one
INSERT INTO mfa_challenges (
    token_hash, user_id, session_id, purpose, csrf_hash, ceremony_json, attempts,
    expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetActiveMFAChallenge :one
SELECT * FROM mfa_challenges WHERE token_hash = ? AND expires_at > ?;

-- name: GetMFAChallengeByUserSessionPurpose :one
SELECT * FROM mfa_challenges
WHERE user_id = ?
  AND session_id = ?
  AND purpose = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: IncrementMFAChallengeAttempts :one
UPDATE mfa_challenges SET attempts = attempts + 1
WHERE token_hash = ? AND attempts < ? RETURNING *;

-- name: ConsumeMFAChallenge :execrows
DELETE FROM mfa_challenges WHERE token_hash = ?;

-- name: ConsumeMFAChallengeGuarded :execrows
DELETE FROM mfa_challenges
WHERE token_hash = ?1
  AND user_id = ?2
  AND purpose = ?3
  AND (session_id = ?4 OR (session_id IS NULL AND ?4 IS NULL))
  AND csrf_hash = ?5
   AND expires_at > ?6
   AND attempts < ?7;

-- name: ConsumeMFAChallengeBySessionGuarded :execrows
DELETE FROM mfa_challenges
WHERE token_hash = ?1
  AND user_id = ?2
  AND purpose = ?3
  AND session_id = ?4
  AND expires_at > ?5;

-- name: PromoteMFAChallengeToWebAuthn :execrows
UPDATE mfa_challenges
SET purpose = sqlc.arg(next_purpose), ceremony_json = sqlc.arg(ceremony_json)
WHERE token_hash = sqlc.arg(token_hash)
  AND user_id = sqlc.arg(user_id)
  AND purpose = sqlc.arg(purpose)
  AND (session_id = sqlc.arg(session_id) OR (session_id IS NULL AND sqlc.arg(session_id) IS NULL))
  AND csrf_hash = sqlc.arg(csrf_hash)
  AND expires_at > sqlc.arg(expires_at)
  AND attempts < sqlc.arg(attempts);

-- name: DeleteMFAChallengesByUserID :execrows
DELETE FROM mfa_challenges WHERE user_id = ?;

-- name: DeleteMFAChallengesBySessionID :execrows
DELETE FROM mfa_challenges WHERE session_id = ?;

-- name: DeleteMFAChallengesByUserSessionPurpose :execrows
DELETE FROM mfa_challenges
WHERE user_id = ?
  AND session_id = ?
  AND purpose = ?;

-- name: DeleteExpiredMFAChallenges :exec
DELETE FROM mfa_challenges
WHERE expires_at < ?
  AND purpose != 'admin_mfa_reset';

-- name: CreateMFARateLimit :one
INSERT INTO mfa_rate_limits (
    user_id, window_started_at, failure_count, blocked_until
) VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetMFARateLimitByUserID :one
SELECT * FROM mfa_rate_limits WHERE user_id = ?;

-- name: UpdateMFARateLimit :one
UPDATE mfa_rate_limits
SET window_started_at = ?, failure_count = ?, blocked_until = ?
WHERE user_id = ? RETURNING *;

-- name: DeleteMFARateLimit :exec
DELETE FROM mfa_rate_limits WHERE user_id = ?;
