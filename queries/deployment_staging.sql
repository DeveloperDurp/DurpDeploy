-- name: AddAgentStagingCapability :exec
INSERT INTO agent_staging_capabilities (agent_id) VALUES (?);

-- name: ClearAgentStagingCapability :exec
DELETE FROM agent_staging_capabilities WHERE agent_id = ?;

-- name: HasStagingInput :one
SELECT COUNT(*) FROM deployment_staging_snapshots
WHERE deployment_id = ? AND step_index = ? AND producer = '';

-- name: GetStagingSnapshot :one
SELECT * FROM deployment_staging_snapshots
WHERE deployment_id = ? AND step_index = ? AND producer = ?;

-- name: ListStagingSnapshots :many
SELECT * FROM deployment_staging_snapshots
WHERE deployment_id = ? ORDER BY step_index, producer;

-- name: CreateStagingSnapshot :exec
INSERT INTO deployment_staging_snapshots (deployment_id, step_index, producer, sha256, size)
VALUES (?, ?, ?, ?, ?);

-- name: CreateStagingChunk :exec
INSERT INTO deployment_staging_chunks (deployment_id, step_index, producer, chunk_index, ciphertext)
VALUES (?, ?, ?, ?, ?);

-- name: GetStagingChunk :one
SELECT ciphertext FROM deployment_staging_chunks
WHERE deployment_id = ? AND step_index = ? AND producer = ? AND chunk_index = ?;

-- name: DeleteStepStaging :exec
DELETE FROM deployment_staging_snapshots WHERE deployment_id = ? AND step_index = ?;

-- name: PurgeTerminalStaging :exec
DELETE FROM deployment_staging_snapshots WHERE deployment_id IN (
    SELECT id FROM deployments WHERE status NOT IN (
      'queued', 'pending', 'pending_approval', 'running', 'publishing_artifact', 'awaiting_artifact_approval'
    )
);
