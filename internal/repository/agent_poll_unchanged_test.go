package repository_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentPollUnchangedCapabilitiesAvoidWrites(t *testing.T) {
	// Given: a durable normalized report and guards against capability writes.
	repo := remoteFixture(t)
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := db.HeartbeatAgentParams{
		ID: "a", Now: ni(200), CertificateFingerprint: agent.CertificateFingerprint,
	}
	report := repository.AgentPollCapabilities{
		PollRequest: agentproto.PollRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV3,
			},
			SupportedInterpreters: []agentproto.Interpreter{
				agentproto.InterpreterPython3, agentproto.InterpreterBash,
			},
		},
		Staging: true, Artifact: true,
	}
	report.ExecutionModes = []agentproto.ExecutionMode{agentproto.ExecutionHost}
	report.ResourcePolicy, err = repo.ResourceLimits().
		AgentPolicy().
		Advertisement()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordAgentPollCapabilities(t.Context(), heartbeat, report); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"agent_interpreters", "agent_execution_modes", "agent_container_runtimes",
		"agent_container_interpreters", "agent_staging_capabilities", "agent_artifact_capabilities",
		"agent_resource_policies",
	} {
		for _, operation := range []string{"INSERT", "DELETE", "UPDATE"} {
			query := fmt.Sprintf(`CREATE TRIGGER reject_%s_%s BEFORE %s ON %s
BEGIN SELECT RAISE(ABORT, 'unchanged capability write'); END`,
				table, operation, operation, table)
			if _, err := repo.DB.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
		}
	}

	// When: the same capability set arrives in another order without staging.
	heartbeat.Now = ni(201)
	report.SupportedInterpreters[0], report.SupportedInterpreters[1] =
		report.SupportedInterpreters[1], report.SupportedInterpreters[0]
	report.Staging = false
	changed, err := repo.RecordAgentPollCapabilities(
		t.Context(),
		heartbeat,
		report,
	)

	// Then: heartbeat advances without rewriting any capability table.
	if err != nil || changed != 1 {
		t.Fatalf("unchanged poll: rows=%d error=%v", changed, err)
	}
	updated, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil || updated.LastHeartbeatAt != heartbeat.Now {
		t.Fatalf("heartbeat=%v error=%v", updated.LastHeartbeatAt, err)
	}
}
