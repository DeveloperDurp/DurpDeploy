-- name: CreateApproval :one
INSERT INTO deployment_approvals (deployment_id, approved_by, approver_user_id, required_approver_role) VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetApprovalByDeployment :one
SELECT * FROM deployment_approvals WHERE deployment_id = ? ORDER BY approved_at ASC LIMIT 1;
