-- +goose Up
CREATE TABLE project_templates (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    source_project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    name NVARCHAR(255) NOT NULL,
    description NVARCHAR(MAX) NOT NULL DEFAULT '',
    snapshot_json NVARCHAR(MAX) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT (DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())),
    UNIQUE (source_project_id, name)
);

-- +goose Down
DROP TABLE project_templates;
