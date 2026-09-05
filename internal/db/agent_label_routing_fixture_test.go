package db_test

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

type agentLabelRoutingFixture struct {
	ctx          context.Context
	conn         *sql.DB
	queries      *db.Queries
	project      db.Project
	localProject db.Project
	environment  db.Environment
	release      db.Release
	catFact      db.AgentLabel
	build        db.AgentLabel
}

func newAgentLabelRoutingFixture(t *testing.T) agentLabelRoutingFixture {
	t.Helper()
	ctx := context.Background()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	queries := db.New(conn)
	project, err := queries.CreateProject(ctx, db.CreateProjectParams{Name: "routing"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	localProject, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "local"},
	)
	if err != nil {
		t.Fatalf("create local project: %v", err)
	}
	environment, err := queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "production"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "v1",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	for _, agent := range []struct{ id, name string }{
		{"agent-a", "Alpha"},
		{"agent-b", "Beta"},
	} {
		_, err := conn.ExecContext(ctx, `
INSERT INTO agents (
    id, name, status, certificate_pem, certificate_fingerprint
) VALUES (?, ?, 'active', 'certificate', ?)
`, agent.id, agent.name, agent.id+"000000000000000000000000000000000000000000000000000000000")
		if err != nil {
			t.Fatalf("create agent %s: %v", agent.id, err)
		}
		_, err = conn.ExecContext(ctx, `
INSERT INTO agent_pairings (
    agent_id, pairing_code_hash, agent_public_identity, agent_pin,
    server_public_identity, server_pin, state, expires_at, paired_at
) VALUES (?, randomblob(32), ?, ?, 'server', ?, 'paired', 1, 1)
`, agent.id, agent.id, agent.id+"111111111111111111111111111111111111111111111111111111111",
			agent.id+"222222222222222222222222222222222222222222222222222222222")
		if err != nil {
			t.Fatalf("pair agent %s: %v", agent.id, err)
		}
	}
	catFact, err := queries.CreateAgentLabel(ctx, db.CreateAgentLabelParams{
		Name: "Cat Fact", NormalizedName: "cat fact",
	})
	if err != nil {
		t.Fatalf("create Cat Fact: %v", err)
	}
	build, err := queries.CreateAgentLabel(ctx, db.CreateAgentLabelParams{
		Name: "Build", NormalizedName: "build",
	})
	if err != nil {
		t.Fatalf("create Build: %v", err)
	}
	for _, membership := range []db.CreateAgentLabelMembershipParams{
		{AgentLabelID: catFact.ID, AgentID: "agent-a"},
		{AgentLabelID: catFact.ID, AgentID: "agent-b"},
		{AgentLabelID: build.ID, AgentID: "agent-a"},
	} {
		if _, err := queries.CreateAgentLabelMembership(ctx, membership); err != nil {
			t.Fatalf("create membership: %v", err)
		}
	}
	return agentLabelRoutingFixture{
		ctx: ctx, conn: conn, queries: queries, project: project,
		localProject: localProject, environment: environment,
		release: release, catFact: catFact, build: build,
	}
}
