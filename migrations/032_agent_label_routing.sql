-- +goose Up
-- +goose StatementBegin

CREATE TABLE agent_labels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 255),
    normalized_name TEXT NOT NULL CHECK (
        normalized_name = lower(trim(name))
    ),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (normalized_name)
);

CREATE TABLE agent_label_memberships (
    agent_label_id INTEGER NOT NULL
        REFERENCES agent_labels(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (agent_label_id, agent_id)
);
CREATE INDEX idx_agent_label_memberships_agent
    ON agent_label_memberships(agent_id);

CREATE TABLE project_execution_policies (
    project_id INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    target_mode TEXT NOT NULL CHECK (target_mode IN ('local', 'label')),
    agent_label_id INTEGER REFERENCES agent_labels(id) ON DELETE RESTRICT,
    agent_strategy TEXT CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (
        (target_mode = 'local' AND agent_label_id IS NULL
            AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL
            AND agent_strategy IS NOT NULL)
    )
);
CREATE INDEX idx_project_execution_policies_label
    ON project_execution_policies(agent_label_id);

CREATE TABLE scheduled_deployment_routing_policies (
    scheduled_deployment_id INTEGER PRIMARY KEY
        REFERENCES scheduled_deployments(id) ON DELETE CASCADE,
    target_mode TEXT NOT NULL CHECK (
        target_mode IN ('inherit', 'local', 'label')
    ),
    agent_label_id INTEGER REFERENCES agent_labels(id) ON DELETE RESTRICT,
    agent_strategy TEXT CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (
        (target_mode IN ('inherit', 'local') AND agent_label_id IS NULL
            AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL
            AND agent_strategy IS NOT NULL)
    )
);
CREATE INDEX idx_scheduled_routing_policies_label
    ON scheduled_deployment_routing_policies(agent_label_id);

ALTER TABLE deployments ADD COLUMN parent_deployment_id INTEGER
    REFERENCES deployments(id) ON DELETE RESTRICT
    CHECK (parent_deployment_id IS NULL OR parent_deployment_id <> id);
ALTER TABLE deployments ADD COLUMN target_agent_id TEXT;
ALTER TABLE deployments ADD COLUMN target_agent_name TEXT CHECK (
    (target_agent_id IS NULL AND target_agent_name IS NULL)
    OR (target_agent_id IS NOT NULL AND target_agent_name IS NOT NULL)
);

CREATE INDEX idx_deployments_parent ON deployments(parent_deployment_id);

CREATE TRIGGER deployments_parent_must_be_root_insert
BEFORE INSERT ON deployments
WHEN NEW.parent_deployment_id IS NOT NULL AND EXISTS (
    SELECT 1 FROM deployments AS parent
    WHERE parent.id = NEW.parent_deployment_id
      AND parent.parent_deployment_id IS NOT NULL
)
BEGIN
    SELECT RAISE(ABORT, 'deployment parent must be a root');
END;

CREATE TRIGGER deployments_parent_must_be_root_update
BEFORE UPDATE OF parent_deployment_id ON deployments
WHEN NEW.parent_deployment_id IS NOT NULL AND EXISTS (
    SELECT 1 FROM deployments AS parent
    WHERE parent.id = NEW.parent_deployment_id
      AND parent.parent_deployment_id IS NOT NULL
)
BEGIN
    SELECT RAISE(ABORT, 'deployment parent must be a root');
END;

CREATE TRIGGER deployment_root_cannot_become_child
BEFORE UPDATE OF parent_deployment_id ON deployments
WHEN NEW.parent_deployment_id IS NOT NULL AND EXISTS (
    SELECT 1 FROM deployments AS child
    WHERE child.parent_deployment_id = NEW.id
)
BEGIN
    SELECT RAISE(ABORT, 'deployment with children must remain a root');
END;

CREATE TABLE deployment_routing_snapshots (
    deployment_id INTEGER PRIMARY KEY
        REFERENCES deployments(id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK (
        source IN ('legacy', 'project', 'request', 'schedule', 'retry', 'redeploy')
    ),
    target_mode TEXT NOT NULL CHECK (target_mode IN ('local', 'label')),
    agent_label_id INTEGER REFERENCES agent_labels(id) ON DELETE RESTRICT,
    agent_label_name TEXT,
    agent_strategy TEXT CHECK (agent_strategy IN ('round_robin', 'all')),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (
        (target_mode = 'local' AND agent_label_id IS NULL
            AND agent_label_name IS NULL AND agent_strategy IS NULL)
        OR (target_mode = 'label' AND agent_label_id IS NOT NULL
            AND agent_label_name IS NOT NULL AND agent_strategy IS NOT NULL)
    )
);
CREATE INDEX idx_deployment_routing_snapshots_label
    ON deployment_routing_snapshots(agent_label_id);

CREATE TABLE deployment_routing_agents (
    deployment_id INTEGER NOT NULL
        REFERENCES deployment_routing_snapshots(deployment_id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    agent_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    PRIMARY KEY (deployment_id, position),
    UNIQUE (deployment_id, agent_id)
);

CREATE TABLE agent_label_cursors (
    agent_label_id INTEGER PRIMARY KEY
        REFERENCES agent_labels(id) ON DELETE CASCADE,
    last_agent_id TEXT,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE scheduled_deployment_occurrences (
    scheduled_deployment_id INTEGER NOT NULL
        REFERENCES scheduled_deployments(id) ON DELETE RESTRICT,
    due_at INTEGER NOT NULL,
    deployment_id INTEGER NOT NULL UNIQUE
        REFERENCES deployments(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (scheduled_deployment_id, due_at)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TEMP TABLE agent_label_routing_down_guard (has_fanout INTEGER);
CREATE TEMP TRIGGER agent_label_routing_down_abort
BEFORE INSERT ON agent_label_routing_down_guard
WHEN NEW.has_fanout = 1
BEGIN
    SELECT RAISE(ABORT, 'cannot downgrade agent label routing with fan-out history');
END;
INSERT INTO agent_label_routing_down_guard (has_fanout)
SELECT EXISTS (
    SELECT 1 FROM deployments WHERE parent_deployment_id IS NOT NULL
);
DROP TRIGGER agent_label_routing_down_abort;
DROP TABLE agent_label_routing_down_guard;

DROP TABLE scheduled_deployment_occurrences;
DROP TABLE agent_label_cursors;
DROP TABLE deployment_routing_agents;
DROP TABLE deployment_routing_snapshots;
DROP TRIGGER deployment_root_cannot_become_child;
DROP TRIGGER deployments_parent_must_be_root_update;
DROP TRIGGER deployments_parent_must_be_root_insert;
DROP INDEX idx_deployments_parent;
ALTER TABLE deployments DROP COLUMN target_agent_name;
ALTER TABLE deployments DROP COLUMN target_agent_id;
ALTER TABLE deployments DROP COLUMN parent_deployment_id;
DROP TABLE scheduled_deployment_routing_policies;
DROP TABLE project_execution_policies;
DROP TABLE agent_label_memberships;
DROP TABLE agent_labels;

-- +goose StatementEnd
