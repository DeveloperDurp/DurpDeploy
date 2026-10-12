-- name: GetDeploymentVariableSnapshot :one
SELECT * FROM deployment_variable_snapshots WHERE deployment_id = ?;

-- name: CreateDeploymentVariableSnapshot :exec
INSERT INTO deployment_variable_snapshots (deployment_id, value) VALUES (?, ?);

-- name: ListAllDeploymentVariableSnapshots :many
SELECT * FROM deployment_variable_snapshots ORDER BY deployment_id;

-- name: UpdateDeploymentVariableSnapshotValue :exec
UPDATE deployment_variable_snapshots SET value = ? WHERE deployment_id = ?;
