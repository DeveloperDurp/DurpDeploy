-- +goose Up
-- +goose StatementBegin

ALTER TABLE approvals RENAME TO deployment_approvals;

ALTER TABLE deployment_approvals ADD COLUMN approver_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE deployment_approvals ADD COLUMN required_approver_role TEXT NOT NULL DEFAULT 'admin';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE deployment_approvals RENAME TO approvals;

-- SQLite <3.35 cannot DROP COLUMN. Leave approver_user_id/required_approver_role
-- in place on Down; matches the precedent set in 003_lifecycles.sql,
-- 005_step_timeout.sql, 006_deployment_notes.sql, 008_approvals.sql.

-- +goose StatementEnd
