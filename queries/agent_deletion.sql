-- Shared sqlc queries use schema-constrained state literals, not Oracle PL/SQL constants.
-- Indexed FK existence checks preserve one deployment row without join duplication.
-- name: LockAgentForDeletion :execrows
UPDATE agents SET updated_at = updated_at -- NOSONAR: intentional write lock
WHERE id = ?;

-- name: CountAgentDeletionBlockers :one
SELECT
  (SELECT COUNT(*) FROM remote_step_runs s WHERE s.agent_id = sqlc.arg(agent_id)
    AND (s.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed') -- NOSONAR: shared SQL state literals and indexed FK existence checks
      OR (s.state = 'cleanup_unconfirmed' AND s.cleanup_confirmed_at IS NULL) -- NOSONAR: shared SQL state literals and indexed FK existence checks
      OR COALESCE(s.log_buffer_ciphertext, '') <> ''))
  + (SELECT COUNT(*) FROM remote_deployment_claims c WHERE c.agent_id = sqlc.arg(agent_id)
    AND (c.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_unconfirmed') -- NOSONAR: shared SQL state literals and indexed FK existence checks
      OR (c.state = 'cleanup_unconfirmed' AND c.cleanup_confirmed_at IS NULL) -- NOSONAR: shared SQL state literals and indexed FK existence checks
      OR COALESCE(c.log_buffer_ciphertext, '') <> ''))
  + (SELECT COUNT(*) FROM deployment_step_attempts a WHERE a.agent_id = sqlc.arg(agent_id)
    AND a.state IN ('claimed', 'started', 'cancel_requested', 'lost', 'cancel_uncertain')); -- NOSONAR: shared SQL state literals and indexed FK existence checks

-- name: CountAgentActiveDeploymentReferences :one
SELECT COUNT(*) FROM deployments d
WHERE (d.status NOT IN ('succeeded', 'failed', 'cancelled', 'rejected', 'expired', 'cleanup_unconfirmed') -- NOSONAR: shared SQL state literals and indexed FK existence checks
  OR (d.status = 'cleanup_unconfirmed' -- NOSONAR: shared SQL state literals and indexed FK existence checks
    AND (d.container_namespace IS NOT NULL
      OR (NOT EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = d.id AND s.cleanup_confirmed_at IS NOT NULL) -- NOSONAR: shared SQL state literals and indexed FK existence checks
      AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = d.id AND c.cleanup_confirmed_at IS NOT NULL) -- NOSONAR: shared SQL state literals and indexed FK existence checks
      AND d.cleanup_confirmed_at IS NULL)
      OR EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = d.id AND s.state = 'cleanup_unconfirmed' AND s.cleanup_confirmed_at IS NULL) -- NOSONAR: shared SQL state literals and indexed FK existence checks
      OR EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = d.id AND c.state = 'cleanup_unconfirmed' AND c.cleanup_confirmed_at IS NULL)))) -- NOSONAR: shared SQL state literals and indexed FK existence checks
  AND (d.assigned_agent_id = sqlc.arg(agent_id)
    OR EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = d.id AND s.agent_id = sqlc.arg(agent_id)) -- NOSONAR: shared SQL state literals and indexed FK existence checks
    OR EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = d.id AND c.agent_id = sqlc.arg(agent_id)) -- NOSONAR: shared SQL state literals and indexed FK existence checks
    OR EXISTS (SELECT 1 FROM deployment_step_attempts a WHERE a.deployment_id = d.id AND a.agent_id = sqlc.arg(agent_id))); -- NOSONAR: shared SQL state literals and indexed FK existence checks

-- name: PreserveAgentDeploymentCleanup :exec
UPDATE deployments SET cleanup_confirmed_at = unixepoch()
WHERE status = 'cleanup_unconfirmed' AND cleanup_confirmed_at IS NULL -- NOSONAR: shared SQL state literals and indexed FK existence checks
  AND container_namespace IS NULL
  AND (EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = deployments.id -- NOSONAR: shared SQL state literals and indexed FK existence checks
      AND s.agent_id = sqlc.arg(agent_id) AND s.cleanup_confirmed_at IS NOT NULL)
    OR EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = deployments.id -- NOSONAR: shared SQL state literals and indexed FK existence checks
      AND c.agent_id = sqlc.arg(agent_id) AND c.cleanup_confirmed_at IS NOT NULL))
  AND NOT EXISTS (SELECT 1 FROM remote_step_runs s WHERE s.deployment_id = deployments.id -- NOSONAR: shared SQL state literals and indexed FK existence checks
      AND s.state = 'cleanup_unconfirmed' AND s.cleanup_confirmed_at IS NULL) -- NOSONAR: shared SQL state literals and indexed FK existence checks
  AND NOT EXISTS (SELECT 1 FROM remote_deployment_claims c WHERE c.deployment_id = deployments.id -- NOSONAR: shared SQL state literals and indexed FK existence checks
      AND c.state = 'cleanup_unconfirmed' AND c.cleanup_confirmed_at IS NULL); -- NOSONAR: shared SQL state literals and indexed FK existence checks

-- name: DeleteAgentRemoteLogSequences :exec
DELETE FROM remote_step_log_sequences WHERE agent_id = ?;

-- name: DeleteAgentRemoteStepRuns :exec
DELETE FROM remote_step_runs WHERE agent_id = ?;

-- name: DeleteAgentRemoteClaims :exec
DELETE FROM remote_deployment_claims WHERE agent_id = ?;

-- name: ClearAgentDispatchPayloads :exec
UPDATE deployment_dispatches SET ciphertext = NULL
WHERE EXISTS (SELECT 1 FROM deployment_step_attempts a -- NOSONAR: shared SQL state literals and indexed FK existence checks
  WHERE a.deployment_id = deployment_dispatches.deployment_id
    AND a.step_index = deployment_dispatches.step_index
    AND a.attempt = deployment_dispatches.attempt AND a.agent_id = ?);

-- name: DetachAgentDeploymentAttempts :exec
UPDATE deployment_step_attempts
SET agent_id = NULL, claim_token_hash = NULL, claim_expires_at = NULL
WHERE agent_id = ?;

-- name: DetachAgentDeployments :exec
UPDATE deployments SET assigned_agent_id = NULL WHERE assigned_agent_id = ?;

-- name: DeleteAgentPairing :exec
DELETE FROM agent_pairings WHERE agent_id = ?;

-- name: DeleteAgentLabels :exec
DELETE FROM agent_labels WHERE agent_id = ?;

-- name: DeleteAgentRegistration :execrows
DELETE FROM agents WHERE id = ?;
