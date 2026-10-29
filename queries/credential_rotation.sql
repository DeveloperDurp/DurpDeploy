-- name: UpdateProjectCredentialRotation :one
UPDATE variables SET
    value = CASE WHEN CAST(sqlc.arg(rotating) AS INTEGER) = 1 THEN sqlc.arg(value) ELSE value END,
    expires_at = sqlc.arg(expires_at), rotation_due_at = sqlc.arg(rotation_due_at),
    rotated_at = CASE WHEN CAST(sqlc.arg(rotating) AS INTEGER) = 1 THEN sqlc.arg(rotated_at) ELSE rotated_at END,
    rotation_revision = rotation_revision + 1
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(owner_id)
    AND rotation_revision = sqlc.arg(revision) AND secret <> 0 AND is_credential = 1
RETURNING *;

-- name: UpdateLifecycleCredentialRotation :one
UPDATE lifecycle_variables SET
    value = CASE WHEN CAST(sqlc.arg(rotating) AS INTEGER) = 1 THEN sqlc.arg(value) ELSE value END,
    expires_at = sqlc.arg(expires_at), rotation_due_at = sqlc.arg(rotation_due_at),
    rotated_at = CASE WHEN CAST(sqlc.arg(rotating) AS INTEGER) = 1 THEN sqlc.arg(rotated_at) ELSE rotated_at END,
    rotation_revision = rotation_revision + 1
WHERE id = sqlc.arg(id) AND lifecycle_id = sqlc.arg(owner_id)
    AND rotation_revision = sqlc.arg(revision) AND secret <> 0 AND is_credential = 1
RETURNING *;

-- name: UpdatePackageCredentialRotation :one
UPDATE package_repositories SET
    credential = CASE WHEN CAST(sqlc.arg(rotating) AS INTEGER) = 1 THEN sqlc.arg(value) ELSE credential END,
    expires_at = sqlc.arg(expires_at), rotation_due_at = sqlc.arg(rotation_due_at),
    rotated_at = CASE WHEN CAST(sqlc.arg(rotating) AS INTEGER) = 1 THEN sqlc.arg(rotated_at) ELSE rotated_at END,
    rotation_revision = rotation_revision + 1
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(owner_id)
    AND rotation_revision = sqlc.arg(revision) AND auth_type <> 'noauth'
RETURNING *;

-- name: ListCredentialReleaseVariables :many
SELECT v.* FROM release_variables v JOIN releases r ON r.id = v.release_id
WHERE r.project_id = sqlc.arg(project_id) AND v.secret <> 0
  AND v.name = sqlc.arg(name)
  AND (v.environment_id = sqlc.narg(environment_id)
       OR (v.environment_id IS NULL AND sqlc.narg(environment_id) IS NULL));

-- name: ListLegacyCredentialDeployments :many
SELECT d.id FROM deployments d
LEFT JOIN deployment_variable_snapshots s ON s.deployment_id = d.id
WHERE d.release_id = ? AND s.deployment_id IS NULL;

-- name: EnrollProjectCredential :one
UPDATE variables SET is_credential = 1, rotation_revision = rotation_revision + 1
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(owner_id)
  AND secret <> 0 AND is_credential = 0 AND rotation_revision = sqlc.arg(revision)
RETURNING *;

-- name: EnrollLifecycleCredential :one
UPDATE lifecycle_variables SET is_credential = 1, rotation_revision = rotation_revision + 1
WHERE id = sqlc.arg(id) AND lifecycle_id = sqlc.arg(owner_id)
  AND secret <> 0 AND is_credential = 0 AND rotation_revision = sqlc.arg(revision)
RETURNING *;

-- name: DeleteProjectCredential :execrows
DELETE FROM variables WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(owner_id)
  AND is_credential = 1 AND rotation_revision = sqlc.arg(revision);

-- name: DeleteLifecycleCredential :execrows
DELETE FROM lifecycle_variables WHERE id = sqlc.arg(id) AND lifecycle_id = sqlc.arg(owner_id)
  AND is_credential = 1 AND rotation_revision = sqlc.arg(revision);
