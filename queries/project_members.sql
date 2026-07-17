-- name: AddProjectMember :exec
INSERT INTO project_members (project_id, user_id, role) VALUES (?, ?, ?)
ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role;

-- name: RemoveProjectMember :exec
DELETE FROM project_members WHERE project_id = ? AND user_id = ?;

-- name: ListProjectMembers :many
SELECT pm.project_id, pm.user_id, pm.role, pm.created_at, u.email, u.name
FROM project_members pm
JOIN users u ON u.id = pm.user_id
WHERE pm.project_id = ?
ORDER BY pm.created_at ASC;

-- name: IsProjectMember :one
SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id = ? AND user_id = ?);

-- name: GetProjectMember :one
SELECT * FROM project_members WHERE project_id = ? AND user_id = ?;

-- name: ListProjectsForUser :many
SELECT p.* FROM projects p
JOIN project_members pm ON pm.project_id = p.id
WHERE pm.user_id = ?
ORDER BY p.created_at DESC;

-- name: ListUsers :many
SELECT id, email, name, role, created_at, updated_at, last_login_at FROM users ORDER BY email;