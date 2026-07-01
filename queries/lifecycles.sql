-- name: ListLifecycles :many
SELECT * FROM lifecycles ORDER BY name;

-- name: GetLifecycle :one
SELECT * FROM lifecycles WHERE id = ?;

-- name: CreateLifecycle :one
INSERT INTO lifecycles (name, description) VALUES (?, ?) RETURNING *;

-- name: UpdateLifecycle :one
UPDATE lifecycles SET name = ?, description = ? WHERE id = ? RETURNING *;

-- name: DeleteLifecycle :exec
DELETE FROM lifecycles WHERE id = ?;

-- name: ListLifecycleStages :many
SELECT * FROM lifecycle_stages WHERE lifecycle_id = ? ORDER BY sort_order;

-- name: GetLifecycleStage :one
SELECT * FROM lifecycle_stages WHERE id = ?;

-- name: CreateLifecycleStage :one
INSERT INTO lifecycle_stages (lifecycle_id, environment_id, sort_order) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateLifecycleStage :one
UPDATE lifecycle_stages SET environment_id = ?, sort_order = ? WHERE id = ? RETURNING *;

-- name: DeleteLifecycleStage :exec
DELETE FROM lifecycle_stages WHERE id = ?;

-- name: DeleteLifecycleStagesByLifecycle :exec
DELETE FROM lifecycle_stages WHERE lifecycle_id = ?;

-- name: ListLifecycleStageEnvironmentIDs :many
SELECT environment_id FROM lifecycle_stages WHERE lifecycle_id = ?;
