-- name: CreateDeploymentVerification :exec
INSERT INTO deployment_verifications (deployment_id, type, target, timeout_seconds, step_index) VALUES (?, ?, ?, ?, ?);

-- name: GetDeploymentVerification :one
SELECT * FROM deployment_verifications WHERE deployment_id = ?;

-- name: StartDeploymentVerification :execrows
UPDATE deployment_verifications SET status = 'running', started_at = unixepoch()
WHERE deployment_id = ? AND status = 'pending';

-- name: FinishDeploymentVerification :exec
UPDATE deployment_verifications SET status = ?, finished_at = unixepoch()
WHERE deployment_id = ? AND status IN ('pending', 'running');

-- name: ReconcileTerminalVerifications :exec
UPDATE deployment_verifications SET status = CASE
    WHEN (SELECT status FROM deployments WHERE id = deployment_id) = 'cancelled' THEN 'cancelled'
    ELSE 'failed' END, finished_at = unixepoch()
WHERE status IN ('pending', 'running') AND EXISTS (
    SELECT 1 FROM deployments WHERE id = deployment_id
    AND status IN ('succeeded', 'failed', 'cancelled', 'rejected', 'expired', 'cleanup_unconfirmed')
);

-- name: MarkReleaseSnapshotLocked :exec
UPDATE releases SET snapshot_locked = 1 WHERE id = ?;

-- name: LockUnusedReleaseSnapshot :execrows
UPDATE releases SET version = version -- NOSONAR: intentional write lock
WHERE id = ? AND snapshot_locked = 0;

-- name: ListDeploymentVerificationTargets :many
SELECT deployment_id, target FROM deployment_verifications;

-- name: UpdateDeploymentVerificationTarget :exec
UPDATE deployment_verifications SET target = ? WHERE deployment_id = ?;

-- name: UpdateEnvironmentVerificationTarget :exec
UPDATE environments SET verification_target = ? WHERE id = ?;
