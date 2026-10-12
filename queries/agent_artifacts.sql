-- name: AddAgentArtifactCapability :exec
INSERT INTO agent_artifact_capabilities (agent_id) VALUES (?);

-- name: ClearAgentArtifactCapability :exec
DELETE FROM agent_artifact_capabilities WHERE agent_id = ?;
