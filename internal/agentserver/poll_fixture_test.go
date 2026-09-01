package agentserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type pollFixture struct {
	*agentServerFixture
	releaseID int64
	envID     int64
}

func newPollFixture(t *testing.T) *pollFixture {
	t.Helper()
	fixture := newAgentServerFixture(t)
	fixture.listener.pollWait = func(context.Context) error { return nil }
	ctx := context.Background()
	project, err := fixture.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name: "poll-project",
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := fixture.repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "poll-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := fixture.repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "poll-v1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	return &pollFixture{
		agentServerFixture: fixture,
		releaseID:          release.ID,
		envID:              environment.ID,
	}
}

func (fixture *pollFixture) activate(
	t *testing.T,
	agentID string,
	identity agenttls.Identity,
) {
	t.Helper()
	activateFixtureAgent(t, fixture.agentServerFixture, agentID, identity)
}

func (fixture *pollFixture) addEligibleAgent(
	t *testing.T,
	agentID string,
) {
	t.Helper()
	pairing, err := fixture.repo.Queries.GetAgentPairing(
		context.Background(),
		agentID,
	)
	if err != nil || pairing.State != "paired" {
		t.Fatalf("paired agent %q = %#v, %v", agentID, pairing, err)
	}
}

func (fixture *pollFixture) createWaitingDeployment(
	t *testing.T,
	payload string,
) int64 {
	t.Helper()
	ctx := context.Background()
	deployment, err := fixture.repo.Queries.CreateDeployment(ctx,
		db.CreateDeploymentParams{
			ReleaseID: fixture.releaseID, EnvironmentID: fixture.envID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	ciphertext, err := fixture.box.Encrypt(payload)
	if err != nil {
		t.Fatalf("encrypt payload: %v", err)
	}
	if _, err := fixture.repo.Queries.CreateDeploymentPayload(
		ctx,
		db.CreateDeploymentPayloadParams{
			DeploymentID: deployment.ID,
			Ciphertext:   ciphertext,
		},
	); err != nil {
		t.Fatalf("create payload: %v", err)
	}
	if _, err := fixture.repo.Queries.CreateDirectDeploymentDispatch(ctx,
		db.CreateDirectDeploymentDispatchParams{
			DeploymentID:    deployment.ID,
			AssignedAgentID: sql.NullString{String: "agent-a", Valid: true},
		},
	); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}
	return deployment.ID
}

func (fixture *pollFixture) createActiveClaim(t *testing.T, agentID string) {
	t.Helper()
	deploymentID := fixture.createWaitingDeployment(t, "active-payload")
	hash := sha256.Sum256([]byte("active-token"))
	if _, err := fixture.repo.Queries.ClaimDeploymentDispatch(
		context.Background(),
		db.ClaimDeploymentDispatchParams{
			AgentID:        sql.NullString{String: agentID, Valid: true},
			ClaimTokenHash: hash[:],
			ClaimExpiresAt: sql.NullInt64{
				Int64: fixture.now.AddDate(0, 0, 1).Unix(),
				Valid: true,
			},
			LastHeartbeatAt: sql.NullInt64{
				Int64: fixture.now.Unix(),
				Valid: true,
			},
			DeploymentID: deploymentID,
		},
	); err != nil {
		t.Fatalf("create active claim: %v", err)
	}
}

func (fixture *pollFixture) revoke(t *testing.T, agentID string) {
	t.Helper()
	if _, err := fixture.repo.Queries.RevokeAgent(context.Background(),
		db.RevokeAgentParams{
			ID:        agentID,
			RevokedAt: sql.NullInt64{Int64: fixture.now.Unix(), Valid: true},
			UpdatedAt: fixture.now.Unix(),
		},
	); err != nil {
		t.Fatalf("revoke agent: %v", err)
	}
}
