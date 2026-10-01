-- name: CreatePackageRepository :one
INSERT INTO package_repositories (project_id, name, url_template, auth_type, username, credential)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetPackageRepository :one
SELECT * FROM package_repositories WHERE id = ?;

-- name: ListPackageRepositories :many
SELECT * FROM package_repositories WHERE project_id = ? ORDER BY name, id;

-- name: UpdatePackageRepository :one
UPDATE package_repositories SET name = ?, url_template = ?, auth_type = ?, username = ?, credential = ?
WHERE id = ? AND project_id = ? RETURNING *;

-- name: PackageRepositoryHasPins :one
SELECT CASE WHEN EXISTS (SELECT 1 FROM release_artifacts r WHERE r.repository_id = ?)
    OR EXISTS (SELECT 1 FROM deployment_artifacts d WHERE d.repository_id = ?) THEN 1 ELSE 0 END;

-- name: DeletePackageRepository :exec
DELETE FROM package_repositories WHERE id = ? AND project_id = ?;

-- name: GetProjectArtifactRepository :one
SELECT p.* FROM package_repositories p
JOIN project_artifact_repositories s ON s.repository_id = p.id WHERE s.project_id = ?;

-- name: DeleteProjectArtifactRepository :exec
DELETE FROM project_artifact_repositories WHERE project_id = ?;

-- name: SelectProjectArtifactRepository :exec
INSERT INTO project_artifact_repositories (project_id, repository_id) VALUES (?, ?);

-- name: CreateReleaseArtifact :exec
INSERT INTO release_artifacts (release_id, repository_id, url, version, sha256, size, source_release_id)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetReleaseArtifact :one
SELECT * FROM release_artifacts WHERE release_id = ?;

-- name: DeleteReleaseArtifact :exec
DELETE FROM release_artifacts WHERE release_id = ?;

-- name: ClearArtifactSourceReleaseByRelease :exec
UPDATE release_artifacts SET source_release_id = NULL WHERE source_release_id = ?;

-- name: GetDeploymentArtifact :one
SELECT * FROM deployment_artifacts WHERE deployment_id = ?;

-- name: CopyReleaseArtifactToDeployment :exec
INSERT INTO deployment_artifacts (deployment_id, repository_id, url, version, sha256, size)
SELECT ?, r.repository_id, r.url, r.version, r.sha256, r.size FROM release_artifacts r WHERE r.release_id = ?;

-- name: CopyDeploymentArtifact :exec
INSERT INTO deployment_artifacts (deployment_id, repository_id, url, version, sha256, size)
SELECT ?, d.repository_id, d.url, d.version, d.sha256, d.size FROM deployment_artifacts d WHERE d.deployment_id = ?;

-- name: CopyReleaseArtifactToRunbook :execrows
INSERT INTO release_artifacts (release_id, repository_id, url, version, sha256, size, source_release_id)
SELECT ?, r.repository_id, r.url, r.version, r.sha256, r.size, r.release_id FROM release_artifacts r WHERE r.release_id = ?;

-- name: DeleteDeploymentArtifact :exec
DELETE FROM deployment_artifacts WHERE deployment_id = ?;

-- name: DeleteProjectReleaseArtifacts :exec
DELETE FROM release_artifacts WHERE release_id IN (SELECT id FROM releases WHERE project_id = ?);

-- name: DeleteProjectDeploymentArtifacts :exec
DELETE FROM deployment_artifacts WHERE deployment_id IN (SELECT d.id FROM deployments d JOIN releases r ON r.id = d.release_id WHERE r.project_id = ?);

-- name: DeleteProjectPackageRepositories :exec
DELETE FROM package_repositories WHERE project_id = ?;

-- name: ClearArtifactSourceRelease :exec
UPDATE release_artifacts SET source_release_id = NULL WHERE source_release_id IN (SELECT id FROM releases WHERE project_id = ?);

-- name: ListPackageRepositoryCredentials :many
SELECT id, credential FROM package_repositories WHERE credential <> '';

-- name: UpdatePackageRepositoryCredential :exec
UPDATE package_repositories SET credential = ? WHERE id = ?;

-- name: LockPackageRepository :execrows
UPDATE package_repositories SET name = name WHERE id = ?; -- NOSONAR: intentional write lock

-- name: UpdatePackageRepositoryKeepCredential :one
UPDATE package_repositories SET name = ?, url_template = ?, auth_type = ?, username = ?
WHERE id = ? AND project_id = ? RETURNING *;

-- name: ListArtifactReleases :many
SELECT r.* FROM releases r JOIN release_artifacts a ON a.release_id = r.id
WHERE r.project_id = ? AND r.kind = 'deployment' ORDER BY r.created_at DESC, r.id DESC;

-- name: HasLiveArtifactClaim :one
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM remote_step_runs s
    JOIN deployments d ON d.id = s.deployment_id
    JOIN agents a ON a.id = s.agent_id
    WHERE s.deployment_id = sqlc.arg(deployment_id)
      AND s.agent_id = sqlc.arg(agent_id)
      AND s.claim_token_hash = sqlc.arg(claim_token_hash)
      AND d.status = 'running' AND a.status = 'active'
      AND EXISTS (SELECT 1 FROM agent_pairings p WHERE p.agent_id = a.id AND p.state = 'paired')
      AND ((s.state = 'claimed' AND s.claim_expires_at > sqlc.arg(now))
        OR (s.state = 'started' AND s.last_heartbeat_at > sqlc.arg(stale_before)))
) THEN 1 ELSE 0 END;
