-- +goose Up
CREATE TABLE deployment_variable_snapshots (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE deployment_variable_snapshots;
