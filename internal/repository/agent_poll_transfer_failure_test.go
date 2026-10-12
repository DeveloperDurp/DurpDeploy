package repository_test

import (
	"errors"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"modernc.org/sqlite"
)

func TestAgentPollTransferWriteFailureRollsBack(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		table     string
		operation string
	}{
		{"staging removal", "agent_staging_capabilities", "DELETE"},
		{"staging insertion", "agent_staging_capabilities", "INSERT"},
		{"package removal", "agent_artifact_capabilities", "DELETE"},
		{"package insertion", "agent_artifact_capabilities", "INSERT"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testAgentPollTransferWriteFailure(
				t,
				scenario.table,
				scenario.operation,
			)
		})
	}
}

func testAgentPollTransferWriteFailure(
	t *testing.T,
	table string,
	operation string,
) {
	t.Helper()
	// Given
	repo := remoteFixture(t)
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), `
		INSERT INTO agent_staging_capabilities(agent_id) VALUES('a');
		INSERT INTO agent_artifact_capabilities(agent_id) VALUES('a')`,
	); err != nil {
		t.Fatal(err)
	}
	trigger := fmt.Sprintf(`
		CREATE TRIGGER reject_transfer_write BEFORE %s ON %s
		BEGIN SELECT RAISE(ABORT, 'transfer write failed'); END`,
		operation, table)
	if _, err := repo.DB.ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}

	// When
	_, err = repo.RecordAgentPollCapabilities(
		t.Context(),
		db.HeartbeatAgentParams{
			ID: "a", Now: ni(200), AgentVersion: ns("new-report"),
			CertificateFingerprint: agent.CertificateFingerprint,
		},
		repository.AgentPollCapabilities{
			PollRequest: agentproto.PollRequest{
				ProtocolEnvelope: agentproto.ProtocolEnvelope{
					Protocol: agentproto.AgentV1,
				},
			},
			Staging: true, Artifact: true,
		},
	)

	// Then
	var databaseError *sqlite.Error
	if !errors.As(err, &databaseError) {
		t.Fatalf("transfer write lost its database error: %v", err)
	}
	updated, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastHeartbeatAt != agent.LastHeartbeatAt ||
		updated.AgentVersion != agent.AgentVersion {
		t.Fatalf("failed report persisted heartbeat: %+v", updated)
	}
	var staging, packages int
	if err := repo.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM agent_staging_capabilities WHERE agent_id='a'),
		(SELECT COUNT(*) FROM agent_artifact_capabilities WHERE agent_id='a')`,
	).Scan(&staging, &packages); err != nil {
		t.Fatal(err)
	}
	if staging != 1 || packages != 1 {
		t.Fatalf("failed report changed capabilities: staging=%d package=%d",
			staging, packages)
	}
}
