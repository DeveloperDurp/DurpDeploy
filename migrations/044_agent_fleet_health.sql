-- +goose Up
ALTER TABLE agents ADD COLUMN draining INTEGER NOT NULL DEFAULT 0
    CHECK (draining IN (0, 1));
ALTER TABLE agents ADD COLUMN health_state TEXT NOT NULL DEFAULT 'unknown'
    CHECK (health_state IN ('unknown', 'healthy', 'stale', 'offline'));
ALTER TABLE agents ADD COLUMN agent_protocol TEXT;

-- +goose Down
-- Refuse rollback: removing drain state could dispatch maintenance work.
CREATE TABLE agent_fleet_health_rollback_refused (
    guard INTEGER CHECK (guard = 0)
);
INSERT INTO agent_fleet_health_rollback_refused VALUES (1);
