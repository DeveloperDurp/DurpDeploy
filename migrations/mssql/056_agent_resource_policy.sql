-- +goose Up
CREATE TABLE agent_resource_policies (
    agent_id NVARCHAR(255) PRIMARY KEY REFERENCES agents(id) ON DELETE NO ACTION,
    advertisement NVARCHAR(256) NOT NULL
);

-- +goose Down
DROP TABLE agent_resource_policies;
