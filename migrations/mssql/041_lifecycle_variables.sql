-- +goose Up
-- +goose StatementBegin
CREATE TABLE lifecycle_variables (
    id BIGINT IDENTITY(1,1) PRIMARY KEY,
    lifecycle_id BIGINT NOT NULL REFERENCES lifecycles(id) ON DELETE CASCADE,
    name NVARCHAR(255) NOT NULL CHECK (LEN(name) BETWEEN 1 AND 255),
    value NVARCHAR(MAX),
    environment_id BIGINT REFERENCES environments(id) ON DELETE CASCADE,
    secret BIGINT NOT NULL DEFAULT 0 CHECK (secret IN (0, 1)),
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE UNIQUE INDEX lifecycle_variables_unscoped
    ON lifecycle_variables(lifecycle_id, name) WHERE environment_id IS NULL;
CREATE UNIQUE INDEX lifecycle_variables_scoped
    ON lifecycle_variables(lifecycle_id, name, environment_id)
    WHERE environment_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
DROP TABLE lifecycle_variables;
