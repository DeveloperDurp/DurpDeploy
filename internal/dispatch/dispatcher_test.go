package dispatch

import (
	"context"
	"durpdeploy/internal/testutil"
	"strings"
	"testing"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteIdentityBypassesAllowlistsForEveryProtocol(t *testing.T) {
	variables := []runner.ResolvedVariable{
		{
			Name:  "KEPT",
			Value: "yes",
		}, {Name: "SECRET", Value: "private", Secret: true},
		{Name: containerenv.EnvironmentNameVariable, Value: "dev"},
		{Name: containerenv.EnvironmentIDVariable, Value: "42"},
	}
	step := db.DeploymentStep{VariableNames: `["KEPT"]`}
	selected, err := selectRemoteVariables([]db.DeploymentStep{step}, variables)
	if err != nil || len(selected) != 3 || selected[0].Name != "KEPT" {
		t.Fatalf("selected=%+v: %v", selected, err)
	}
	for _, protocol := range []agentproto.ProtocolVersion{agentproto.AgentV1, agentproto.AgentV2, agentproto.AgentV3} {
		names, err := remoteClaimVariableNames(step, selected, protocol)
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		if protocol == agentproto.AgentV3 && len(names) != 3 ||
			protocol != agentproto.AgentV3 && len(names) != 0 {
			t.Fatalf("%s names=%v", protocol, names)
		}
	}
}

func TestSelectRemoteVariablesUsesStepAllowlists(t *testing.T) {
	variables := []runner.ResolvedVariable{
		{Name: "FIRST", Value: "one"},
		{Name: "SECRET", Value: "hidden", Secret: true},
		{Name: "SECOND", Value: "two"},
	}
	selected, err := selectRemoteVariables([]db.DeploymentStep{
		{Name: "one", VariableNames: `["SECOND"]`},
		{Name: "two", VariableNames: `["FIRST"]`},
	}, variables)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].Name != "FIRST" ||
		selected[1].Name != "SECOND" {
		t.Fatalf("selected variables = %+v", selected)
	}
	all, err := selectRemoteVariables(
		[]db.DeploymentStep{{Name: "none", VariableNames: `[]`}},
		variables,
	)
	if err != nil || len(all) != len(variables) {
		t.Fatalf("default variables = %+v: %v", all, err)
	}
}

func TestSelectRemoteVariablesRejectsUnavailableAllowlistEntries(t *testing.T) {
	_, err := selectRemoteVariables(
		[]db.DeploymentStep{
			{Name: "deploy", VariableNames: `["MISSING","AVAILABLE"]`},
		},
		[]runner.ResolvedVariable{{Name: "AVAILABLE", Value: "yes"}},
	)
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("missing allowlisted variable accepted: %v", err)
	}
}

func TestRuntimeMaintenanceExpiresPairings(t *testing.T) {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, conn)
	repo := repository.New(conn)
	ctx := context.Background()
	_, err = repo.Queries.CreateAgent(ctx, db.CreateAgentParams{
		ID: "expired", Name: "expired", Endpoint: "https://agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Queries.CreateAgentPairing(ctx, db.CreateAgentPairingParams{
		AgentID: "expired", PairingCodeHash: make([]byte, 32),
		AgentPublicIdentity: "fixture", AgentPin: strings.Repeat("a", 64),
		ExpiresAt: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(ctx, `
		INSERT INTO agents (id, name, endpoint) VALUES ('committing', 'committing', 'https://agent-2');
		INSERT INTO agent_pairings (
			agent_id, pairing_code_hash, agent_public_identity, agent_pin,
			server_public_identity, server_pin, encrypted_identity, state,
			expires_at, server_pull_endpoint
		) VALUES (
			'committing', ?, 'agent-cert', ?, 'server-cert', ?,
			'encrypted', 'committing', 100, 'https://server'
		)`, []byte(strings.Repeat("d", 32)), strings.Repeat("b", 64),
		strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if err := New(repo).Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	pairing, err := repo.Queries.GetAgentPairing(ctx, "expired")
	if err != nil || pairing.State != "expired" {
		t.Fatalf("pairing state=%s err=%v", pairing.State, err)
	}
	committing, err := repo.Queries.GetAgentPairing(ctx, "committing")
	if err != nil || committing.State != "committing" {
		t.Fatalf("committing state=%s err=%v", committing.State, err)
	}
	t.Log("maintenance pass: expired pairing at deadline=100")
}
