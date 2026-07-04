-- name: CreateApproval :one
INSERT INTO approvals (deployment_id, approved_by) VALUES (?, ?) RETURNING *;

-- name: GetApprovalByDeployment :one
SELECT * FROM approvals WHERE deployment_id = ? ORDER BY approved_at ASC LIMIT 1;
