-- +goose Up
ALTER TABLE agents ADD draining BIGINT NOT NULL DEFAULT 0
    CHECK (draining IN (0, 1));
ALTER TABLE agents ADD health_state NVARCHAR(16) NOT NULL DEFAULT 'unknown'
    CHECK (health_state IN ('unknown', 'healthy', 'stale', 'offline'));
ALTER TABLE agents ADD agent_protocol NVARCHAR(16);

-- +goose Down
CREATE TABLE agent_fleet_health_rollback_refused (
    guard BIGINT CHECK (guard = 0)
);
INSERT INTO agent_fleet_health_rollback_refused VALUES (1);
