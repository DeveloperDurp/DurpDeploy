package migrate

import (
	"database/sql"
	"testing"
)

var remoteAgentTableNames = []string{
	"agent_pools",
	"agents",
	"agent_pool_memberships",
	"agent_tags",
	"environment_agent_policies",
	"agent_enrollment_tokens",
	"deployment_payloads",
	"deployment_dispatches",
	"agent_events",
}

var remoteAgentIndexNames = []string{
	"idx_deployment_logs_deployment_sequence",
	"idx_agent_pools_name",
	"idx_agents_certificate_fingerprint",
	"idx_agents_status",
	"idx_agents_last_heartbeat_at",
	"idx_agent_pool_memberships_agent_id",
	"idx_agent_tags_agent_id",
	"idx_environment_agent_policies_pool_id",
	"idx_agent_enrollment_tokens_agent_id",
	"idx_agent_enrollment_tokens_expires_at",
	"idx_deployment_dispatches_agent_state",
	"idx_deployment_dispatches_claim_token_hash",
	"idx_deployment_dispatches_state_claim_expires",
	"idx_agent_events_agent_created_at",
	"idx_agent_events_deployment_created_at",
}

func seedRemoteAgentDeployment(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		"INSERT INTO projects (name) VALUES ('remote-agent-project')",
		"INSERT INTO environments (name) VALUES ('remote-agent-environment')",
		"INSERT INTO releases (project_id, version, steps_json) VALUES (1, 'remote-agent-release', '[]')",
		"INSERT INTO deployments (release_id, environment_id, status) VALUES (1, 1, 'pending')",
	} {
		_, err := conn.Exec(statement)
		requireNoError(t, err, "seed remote-agent deployment")
	}
}
