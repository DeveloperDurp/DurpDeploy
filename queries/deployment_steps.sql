-- name: FreezeDeploymentStepSource :execrows
INSERT INTO deployment_step_sources (deployment_id, steps_json)
SELECT d.id, r.steps_json FROM deployments d JOIN releases r ON r.id = d.release_id
WHERE d.id = sqlc.arg(deployment_id) AND d.status IN ('pending', 'pending_approval')
  AND NOT EXISTS (SELECT 1 FROM deployment_step_sources s WHERE s.deployment_id = d.id);

-- name: FreezeDeploymentStepSourceJSON :execrows
INSERT INTO deployment_step_sources (deployment_id, steps_json)
SELECT d.id, sqlc.arg(steps_json) FROM deployments d
WHERE d.id = sqlc.arg(deployment_id) AND d.status IN ('pending', 'pending_approval')
  AND NOT EXISTS (SELECT 1 FROM deployment_step_sources s WHERE s.deployment_id = d.id);

-- name: GetDeploymentStepSource :one
SELECT * FROM deployment_step_sources WHERE deployment_id = ?;

-- name: CreateDeploymentStep :execrows
INSERT INTO deployment_steps (deployment_id, step_index, source_step_id, name, script_body, timeout_seconds, max_retries, execution_target, interpreter)
SELECT sqlc.arg(deployment_id), sqlc.arg(step_index), sqlc.narg(source_step_id), sqlc.arg(name),
    sqlc.arg(script_body), sqlc.arg(timeout_seconds), sqlc.arg(max_retries), sqlc.arg(execution_target),
    COALESCE(NULLIF(CAST(sqlc.arg(interpreter) AS TEXT), ''), 'bash')
WHERE EXISTS (SELECT 1 FROM deployments WHERE id = sqlc.arg(deployment_id) AND status IN ('pending', 'pending_approval'))
  AND NOT EXISTS (SELECT 1 FROM deployment_step_sources WHERE deployment_id = sqlc.arg(deployment_id))
  AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts WHERE deployment_id = sqlc.arg(deployment_id));

-- name: AddDeploymentStepSelector :execrows
INSERT INTO deployment_step_selectors (deployment_id, step_index, label)
SELECT sqlc.arg(deployment_id), sqlc.arg(step_index), sqlc.arg(label)
WHERE EXISTS (SELECT 1 FROM deployments WHERE id = sqlc.arg(deployment_id) AND status IN ('pending', 'pending_approval'))
  AND NOT EXISTS (SELECT 1 FROM deployment_step_sources WHERE deployment_id = sqlc.arg(deployment_id))
  AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts WHERE deployment_id = sqlc.arg(deployment_id));

-- name: ListDeploymentSteps :many
SELECT * FROM deployment_steps WHERE deployment_id = ? ORDER BY step_index;

-- name: ListDeploymentStepSelectors :many
SELECT label FROM deployment_step_selectors WHERE deployment_id = ? AND step_index = ? ORDER BY label;

-- The cursor is the first immutable step without a successful attempt.
-- name: GetDeploymentStepCursor :one
SELECT s.* FROM deployment_steps s
WHERE s.deployment_id = sqlc.arg(deployment_id)
  AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts a WHERE a.deployment_id = s.deployment_id
      AND a.step_index = s.step_index AND a.state = 'succeeded')
ORDER BY s.step_index LIMIT 1;

-- name: CreateDeploymentStepAttempt :execrows
INSERT INTO deployment_step_attempts (deployment_id, step_index, attempt, wait_deadline)
SELECT s.deployment_id, s.step_index, sqlc.arg(attempt), sqlc.arg(wait_deadline)
FROM deployment_steps s JOIN deployments d ON d.id = s.deployment_id
WHERE s.deployment_id = sqlc.arg(deployment_id) AND s.step_index = sqlc.arg(step_index)
  AND d.status IN ('pending', 'running') AND sqlc.arg(wait_deadline) > CAST(sqlc.arg(now) AS INTEGER)
  AND CAST(sqlc.arg(attempt) AS INTEGER) > 0 AND sqlc.arg(attempt) <= s.max_retries + 1
  AND NOT EXISTS (SELECT 1 FROM deployment_steps earlier WHERE earlier.deployment_id = s.deployment_id
      AND earlier.step_index < s.step_index AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts done
          WHERE done.deployment_id = earlier.deployment_id AND done.step_index = earlier.step_index AND done.state = 'succeeded'))
  AND sqlc.arg(attempt) = 1 + (SELECT COUNT(*) FROM deployment_step_attempts a
      WHERE a.deployment_id = s.deployment_id AND a.step_index = s.step_index)
  AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts a WHERE a.deployment_id = s.deployment_id
      AND a.step_index = s.step_index AND a.state != 'failed');

-- name: GetDeploymentStepAttempt :one
SELECT * FROM deployment_step_attempts WHERE deployment_id = ? AND step_index = ? AND attempt = ?;

-- name: ListDeploymentStepAttempts :many
SELECT * FROM deployment_step_attempts WHERE deployment_id = ? ORDER BY step_index, attempt;

-- name: StartLocalDeploymentStep :execrows
UPDATE deployment_step_attempts SET state = 'started', started_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index) AND deployment_step_attempts.attempt = sqlc.arg(attempt)
  AND state = 'waiting' AND agent_id IS NULL AND wait_deadline > sqlc.arg(now)
  AND EXISTS (SELECT 1 FROM deployment_steps s WHERE s.deployment_id = deployment_step_attempts.deployment_id
      AND s.step_index = deployment_step_attempts.step_index AND s.execution_target = 'local')
  AND EXISTS (SELECT 1 FROM deployments d WHERE d.id = deployment_step_attempts.deployment_id AND d.status IN ('pending', 'running'));

-- name: FinishLocalDeploymentStep :execrows
UPDATE deployment_step_attempts SET state = sqlc.arg(state), reason = sqlc.narg(reason),
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE deployment_step_attempts.deployment_id = sqlc.arg(deployment_id) AND deployment_step_attempts.step_index = sqlc.arg(step_index) AND deployment_step_attempts.attempt = sqlc.arg(attempt)
  AND agent_id IS NULL AND state = 'started' AND (sqlc.arg(state) = 'succeeded' OR sqlc.arg(state) = 'failed')
  AND EXISTS (SELECT 1 FROM deployments d WHERE d.id = deployment_step_attempts.deployment_id AND d.status IN ('pending', 'running'));

-- name: FinishCompletedDeployment :execrows
UPDATE deployments SET status = 'succeeded', finished_at = unixepoch()
WHERE id = sqlc.arg(deployment_id) AND status IN ('pending', 'running')
  AND NOT EXISTS (SELECT 1 FROM deployment_steps s WHERE s.deployment_id = deployments.id
      AND NOT EXISTS (SELECT 1 FROM deployment_step_attempts a WHERE a.deployment_id = s.deployment_id
          AND a.step_index = s.step_index AND a.state = 'succeeded'));
