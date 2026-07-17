-- +goose Up
-- +goose StatementBegin

-- ponytail: existing projects are inaccessible to non-admin users until
-- members are added via the Members tab. Add the operator (and any teammates)
-- to each project after the migration. Global admins bypass the membership
-- check, so day-1 operations are not blocked.
CREATE TABLE IF NOT EXISTS project_members (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('admin', 'deployer')),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_project_members_user_id ON project_members(user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_project_members_user_id;
DROP TABLE IF EXISTS project_members;

-- +goose StatementEnd