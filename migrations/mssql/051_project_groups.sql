-- +goose Up
CREATE TABLE project_groups (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    name NVARCHAR(200) NOT NULL,
    name_key NVARCHAR(200) COLLATE Latin1_General_100_BIN2 NOT NULL UNIQUE
);
ALTER TABLE projects ADD group_id BIGINT NULL
    CONSTRAINT FK_projects_group REFERENCES project_groups(id) ON DELETE SET NULL;
CREATE INDEX idx_projects_group ON projects(group_id);

-- +goose Down
DROP INDEX idx_projects_group ON projects;
ALTER TABLE projects DROP CONSTRAINT FK_projects_group;
ALTER TABLE projects DROP COLUMN group_id;
DROP TABLE project_groups;
