-- name: LockDeploymentSnapshot :execrows
UPDATE deployments SET status = status WHERE id = sqlc.arg(deployment_id) AND status IN ('pending', 'pending_approval')
AND NOT EXISTS (SELECT 1 FROM deployment_step_sources WHERE deployment_id = sqlc.arg(deployment_id))
AND NOT EXISTS (SELECT 1 FROM deployment_steps WHERE deployment_id = sqlc.arg(deployment_id));

-- name: LockDeploymentApproval :execrows
UPDATE deployments SET status = status
WHERE id = sqlc.arg(deployment_id) AND status = 'pending_approval';

-- name: ApproveDeploymentStatus :execrows
UPDATE deployments SET status = 'pending'
WHERE id = sqlc.arg(deployment_id) AND status = 'pending_approval';

-- name: CancelStepDeployment :execrows
UPDATE deployments SET status = 'cancelled', finished_at = unixepoch()
WHERE id = ? AND status IN ('pending', 'running');

-- name: ReplaceDeploymentDispatch :execrows
UPDATE deployment_dispatches SET ciphertext = sqlc.narg(ciphertext), updated_at = unixepoch()
WHERE deployment_dispatches.deployment_id = sqlc.arg(deployment_id) AND deployment_dispatches.step_index = sqlc.arg(step_index) AND deployment_dispatches.attempt = sqlc.arg(attempt)
AND EXISTS (SELECT 1 FROM deployment_step_attempts a
    WHERE a.deployment_id = deployment_dispatches.deployment_id AND a.step_index = deployment_dispatches.step_index
    AND a.attempt = deployment_dispatches.attempt AND a.agent_id = sqlc.arg(agent_id)
    AND a.claim_token_hash = sqlc.arg(claim_token_hash) AND a.state = 'claimed');
