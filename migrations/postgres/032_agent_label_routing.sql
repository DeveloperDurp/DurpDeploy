-- +goose Up
-- +goose StatementBegin

CREATE TABLE agent_labels (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 255),
    normalized_name TEXT NOT NULL CHECK (normalized_name = lower(btrim(name))),
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    UNIQUE (normalized_name)
);
CREATE TABLE agent_label_memberships (
    agent_label_id BIGINT NOT NULL REFERENCES agent_labels(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    PRIMARY KEY (agent_label_id, agent_id)
);
CREATE INDEX idx_agent_label_memberships_agent ON agent_label_memberships(agent_id);

CREATE TABLE project_execution_policies (
    project_id BIGINT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    target_mode TEXT NOT NULL CHECK (target_mode IN ('local', 'label')),
    agent_label_id BIGINT REFERENCES agent_labels(id) ON DELETE RESTRICT,
    agent_strategy TEXT CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    CHECK ((target_mode = 'local' AND agent_label_id IS NULL AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL AND agent_strategy IS NOT NULL))
);
CREATE INDEX idx_project_execution_policies_label ON project_execution_policies(agent_label_id);

CREATE TABLE scheduled_deployment_routing_policies (
    scheduled_deployment_id BIGINT PRIMARY KEY REFERENCES scheduled_deployments(id) ON DELETE CASCADE,
    target_mode TEXT NOT NULL CHECK (target_mode IN ('inherit', 'local', 'label')),
    agent_label_id BIGINT REFERENCES agent_labels(id) ON DELETE RESTRICT,
    agent_strategy TEXT CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    CHECK ((target_mode IN ('inherit', 'local') AND agent_label_id IS NULL AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL AND agent_strategy IS NOT NULL))
);
CREATE INDEX idx_scheduled_routing_policies_label ON scheduled_deployment_routing_policies(agent_label_id);

ALTER TABLE deployments ADD COLUMN parent_deployment_id BIGINT
    REFERENCES deployments(id) ON DELETE RESTRICT;
ALTER TABLE deployments ADD COLUMN target_agent_id TEXT;
ALTER TABLE deployments ADD COLUMN target_agent_name TEXT;
ALTER TABLE deployments ADD CONSTRAINT ck_deployments_not_self_parent
    CHECK (parent_deployment_id IS NULL OR parent_deployment_id <> id);
ALTER TABLE deployments ADD CONSTRAINT ck_deployments_target_agent_identity
    CHECK ((target_agent_id IS NULL AND target_agent_name IS NULL)
        OR (target_agent_id IS NOT NULL AND target_agent_name IS NOT NULL));
CREATE INDEX idx_deployments_parent ON deployments(parent_deployment_id);

CREATE FUNCTION enforce_deployment_root_parent() RETURNS trigger AS $$
BEGIN
    IF NEW.parent_deployment_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM deployments WHERE id = NEW.parent_deployment_id
          AND parent_deployment_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'deployment parent must be a root';
    END IF;
    IF NEW.parent_deployment_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM deployments WHERE parent_deployment_id = NEW.id
    ) THEN
        RAISE EXCEPTION 'deployment with children must remain a root';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER deployments_root_parent_guard
BEFORE INSERT OR UPDATE OF parent_deployment_id ON deployments
FOR EACH ROW EXECUTE FUNCTION enforce_deployment_root_parent();

CREATE TABLE deployment_routing_snapshots (
    deployment_id BIGINT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK (source IN ('legacy', 'project', 'request', 'schedule', 'retry', 'redeploy')),
    target_mode TEXT NOT NULL CHECK (target_mode IN ('local', 'label')),
    agent_label_id BIGINT REFERENCES agent_labels(id) ON DELETE RESTRICT,
    agent_label_name TEXT,
    agent_strategy TEXT CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    CHECK ((target_mode = 'local' AND agent_label_id IS NULL AND agent_label_name IS NULL AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL AND agent_label_name IS NOT NULL AND agent_strategy IS NOT NULL))
);
CREATE INDEX idx_deployment_routing_snapshots_label ON deployment_routing_snapshots(agent_label_id);
CREATE TABLE deployment_routing_agents (
    deployment_id BIGINT NOT NULL REFERENCES deployment_routing_snapshots(deployment_id) ON DELETE CASCADE,
    position BIGINT NOT NULL CHECK (position >= 0),
    agent_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    PRIMARY KEY (deployment_id, position),
    UNIQUE (deployment_id, agent_id)
);
CREATE TABLE agent_label_cursors (
    agent_label_id BIGINT PRIMARY KEY REFERENCES agent_labels(id) ON DELETE CASCADE,
    last_agent_id TEXT,
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
);
CREATE TABLE scheduled_deployment_occurrences (
    scheduled_deployment_id BIGINT NOT NULL REFERENCES scheduled_deployments(id) ON DELETE RESTRICT,
    due_at BIGINT NOT NULL,
    deployment_id BIGINT NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE RESTRICT,
    created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
    PRIMARY KEY (scheduled_deployment_id, due_at)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM deployments WHERE parent_deployment_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot downgrade agent label routing with fan-out history';
    END IF;
END $$;
DROP TABLE scheduled_deployment_occurrences;
DROP TABLE agent_label_cursors;
DROP TABLE deployment_routing_agents;
DROP TABLE deployment_routing_snapshots;
DROP TRIGGER deployments_root_parent_guard ON deployments;
DROP FUNCTION enforce_deployment_root_parent();
DROP INDEX idx_deployments_parent;
ALTER TABLE deployments DROP CONSTRAINT ck_deployments_not_self_parent;
ALTER TABLE deployments DROP CONSTRAINT ck_deployments_target_agent_identity;
ALTER TABLE deployments DROP COLUMN target_agent_name;
ALTER TABLE deployments DROP COLUMN target_agent_id;
ALTER TABLE deployments DROP COLUMN parent_deployment_id;
DROP TABLE scheduled_deployment_routing_policies;
DROP TABLE project_execution_policies;
DROP TABLE agent_label_memberships;
DROP TABLE agent_labels;

-- +goose StatementEnd
