package dispatch

import (
	"context"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"fmt"
	"sort"
	"testing"
)

type routingFixture struct {
	repo        *repository.Repository
	project     db.Project
	environment db.Environment
	release     db.Release
	label       db.AgentLabel
}

func newRoutingFixture(t *testing.T, agentCount int) routingFixture {
	t.Helper()
	repo, _ := newPayloadRepository(t)
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "routing"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "production"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	label, err := repo.Queries.CreateAgentLabel(ctx, db.CreateAgentLabelParams{
		Name: "Builders", NormalizedName: "builders",
	})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	for i := range agentCount {
		id := fmt.Sprintf("agent-%c", 'a'+i)
		createEligibleAgent(t, repo, label.ID, id)
	}
	if agentCount > 0 {
		_, err = repo.Queries.AssignAgentToEnvironment(
			ctx,
			db.AssignAgentToEnvironmentParams{
				EnvironmentID: environment.ID, AgentID: "agent-a", UpdatedAt: 1,
			},
		)
		if err != nil {
			t.Fatalf("assign legacy agent: %v", err)
		}
	}
	return routingFixture{
		repo: repo, project: project, environment: environment,
		release: release, label: label,
	}
}

func createEligibleAgent(
	t *testing.T,
	repo *repository.Repository,
	labelID int64,
	id string,
) {
	t.Helper()
	ctx := context.Background()
	_, err := repo.DB.ExecContext(ctx, `
INSERT INTO agents (
    id, name, status, certificate_pem, certificate_fingerprint,
    last_heartbeat_at
) VALUES (?, ?, 'active', 'certificate', ?, NULL)
`, id, id, id+"000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("create agent %s: %v", id, err)
	}
	_, err = repo.DB.ExecContext(ctx, `
INSERT INTO agent_pairings (
    agent_id, pairing_code_hash, agent_public_identity, agent_pin,
    server_public_identity, server_pin, state, expires_at, paired_at
) VALUES (?, randomblob(32), ?, ?, 'server', ?, 'paired', 1, 1)
`, id, id, id+"111111111111111111111111111111111111111111111111111111111",
		id+"222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("pair agent %s: %v", id, err)
	}
	_, err = repo.Queries.CreateAgentLabelMembership(
		ctx,
		db.CreateAgentLabelMembershipParams{
			AgentLabelID: labelID, AgentID: id,
		},
	)
	if err != nil {
		t.Fatalf("create membership %s: %v", id, err)
	}
}

func (fixture routingFixture) createDeployment(t *testing.T) db.Deployment {
	t.Helper()
	deployment, err := fixture.repo.Queries.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID:     fixture.release.ID,
			EnvironmentID: fixture.environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	return deployment
}

func agentIDs(agents []Agent) []string {
	ids := make([]string, len(agents))
	for i, agent := range agents {
		ids[i] = agent.ID
	}
	sort.Strings(ids)
	return ids
}
