-- name: GetArtifactGateRun :one
SELECT * FROM artifact_gate_runs WHERE deployment_id = ?;

-- name: CreateArtifactGateRun :exec
INSERT INTO artifact_gate_runs (deployment_id) VALUES (?);

-- name: ClaimArtifactGateRun :execrows
UPDATE artifact_gate_runs SET claimed = 1
WHERE deployment_id = ? AND claimed = 0
AND EXISTS (SELECT 1 FROM deployments WHERE id = artifact_gate_runs.deployment_id AND status = 'pending');

-- name: AdvanceArtifactGateRun :exec
UPDATE artifact_gate_runs SET next_step = ?, claimed = 0 WHERE deployment_id = ?;

-- name: CreateArtifactGate :exec
INSERT INTO artifact_gates (deployment_id, step_index, status, artifact_path, artifact_sha256, bundle_sha256, bundle_size, review, created_at, expires_at)
VALUES (?, ?, 'awaiting', ?, ?, ?, ?, ?, ?, ?);

-- name: GetArtifactGate :one
SELECT * FROM artifact_gates WHERE deployment_id = ? AND step_index = ?;

-- name: ListArtifactGates :many
SELECT * FROM artifact_gates WHERE deployment_id = ? ORDER BY step_index;

-- name: CreateArtifactGateChunk :exec
INSERT INTO artifact_gate_chunks (deployment_id, step_index, chunk_index, ciphertext) VALUES (?, ?, ?, ?);

-- name: GetArtifactGateChunk :one
SELECT ciphertext FROM artifact_gate_chunks WHERE deployment_id = ? AND step_index = ? AND chunk_index = ?;

-- name: ApproveArtifactGate :execrows
UPDATE artifact_gates SET status = 'approved', approved_by = ?, approved_at = ?
WHERE deployment_id = ? AND step_index = ? AND status = 'awaiting'
AND artifact_sha256 = ? AND revision = ? AND expires_at > sqlc.arg(now);

-- name: FinishArtifactGates :exec
UPDATE artifact_gates SET status = ? WHERE deployment_id = ? AND status = 'awaiting';

-- name: LockGateEnvironment :execrows
UPDATE environments SET name = name -- NOSONAR: intentional admission lock
WHERE id = ?;

-- name: CountGateEnvironmentConflicts :one
SELECT COUNT(*) FROM deployments d
WHERE d.environment_id = sqlc.arg(environment_id) AND d.id != sqlc.arg(deployment_id)
AND d.status IN ('pending', 'running', 'publishing_artifact', 'awaiting_artifact_approval', 'cleanup_unconfirmed')
AND (sqlc.arg(gated) = 1 OR EXISTS (SELECT 1 FROM artifact_gate_runs g WHERE g.deployment_id = d.id));

-- name: LockArtifactGateDeployment :execrows
UPDATE deployments SET status = status -- NOSONAR: intentional gate lock
WHERE id = ?;

-- name: ListArtifactGateChunkKeys :many
SELECT deployment_id, step_index, chunk_index FROM artifact_gate_chunks
ORDER BY deployment_id, step_index, chunk_index;

-- name: UpdateArtifactGateChunkCiphertext :exec
UPDATE artifact_gate_chunks SET ciphertext = sqlc.arg(ciphertext)
WHERE deployment_id = sqlc.arg(deployment_id)
AND step_index = sqlc.arg(step_index) AND chunk_index = sqlc.arg(chunk_index);

-- name: PauseArtifactGateDeployment :execrows
UPDATE deployments SET status = 'awaiting_artifact_approval' WHERE id = ? AND status = 'publishing_artifact';

-- name: PublishArtifactGateDeployment :execrows
UPDATE deployments SET status = 'publishing_artifact' WHERE id = ? AND status = 'running';

-- name: ResumeArtifactGateDeployment :execrows
UPDATE deployments SET status = 'pending' WHERE id = ? AND status = 'awaiting_artifact_approval';

-- name: CreateArtifactGateImage :exec
INSERT INTO artifact_gate_images (deployment_id, step_index, image_id) VALUES (?, ?, ?);

-- name: GetArtifactGateImage :one
SELECT image_id FROM artifact_gate_images WHERE deployment_id = ? AND step_index = ?;

-- name: DeleteExpiredArtifactGateChunks :exec
DELETE FROM artifact_gate_chunks WHERE EXISTS (
    SELECT 1 FROM deployments d WHERE d.id = artifact_gate_chunks.deployment_id
    AND d.finished_at < sqlc.arg(cutoff) AND d.status IN ('succeeded', 'failed', 'cancelled', 'rejected', 'expired')
);

-- name: ExpireArtifactGateDeployments :exec
UPDATE deployments SET status = 'expired', finished_at = sqlc.arg(now)
WHERE status IN ('awaiting_artifact_approval', 'pending')
AND EXISTS (SELECT 1 FROM artifact_gates g JOIN artifact_gate_runs r ON r.deployment_id = g.deployment_id
WHERE g.deployment_id = deployments.id AND g.step_index = r.next_step - 1 AND g.expires_at <= sqlc.arg(now));

-- name: ExpireArtifactGates :exec
UPDATE artifact_gates SET status = 'expired'
WHERE status IN ('awaiting', 'approved') AND expires_at <= sqlc.arg(now)
AND EXISTS (SELECT 1 FROM deployments d JOIN artifact_gate_runs r ON r.deployment_id = d.id
WHERE d.id = artifact_gates.deployment_id AND d.status = 'expired' AND artifact_gates.step_index = r.next_step - 1);

-- name: CancelTerminalArtifactGates :exec
UPDATE artifact_gates SET status = 'cancelled' WHERE status = 'awaiting'
AND EXISTS (SELECT 1 FROM deployments d WHERE d.id = artifact_gates.deployment_id
AND d.status IN ('failed', 'cancelled', 'cleanup_unconfirmed'));
