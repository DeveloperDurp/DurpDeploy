-- +goose Up
CREATE TABLE project_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    name_key TEXT NOT NULL UNIQUE
);
ALTER TABLE projects ADD COLUMN group_id INTEGER
    REFERENCES project_groups(id) ON DELETE SET NULL;
CREATE INDEX idx_projects_group ON projects(group_id);

-- +goose Down
DROP INDEX idx_projects_group;
ALTER TABLE projects DROP COLUMN group_id;
DROP TABLE project_groups;
