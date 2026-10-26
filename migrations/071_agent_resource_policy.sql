-- +goose Up
CREATE TABLE agent_resource_policies (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    advertisement TEXT NOT NULL
);

-- +goose Down
DROP TABLE agent_resource_policies;
