-- +goose Up
CREATE TABLE deployment_variable_snapshots (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    value NVARCHAR(MAX) NOT NULL
);

-- +goose Down
DROP TABLE deployment_variable_snapshots;
