package agentserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"testing"

	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/db"
)

type pollFixture struct {
	*enrollmentFixture
	poolID    int64
	poolName  string
	releaseID int64
	envID     int64
}

func newPollFixture(t *testing.T) *pollFixture {
	t.Helper()
	enrollment := newEnrollmentFixture(t)
	enrollment.listener.pollWait = func(context.Context) error { return nil }
	ctx := context.Background()
	project, err := enrollment.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name: "poll-project",
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := enrollment.repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "poll-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := enrollment.repo.Queries.CreateRelease(
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
	pool, err := enrollment.repo.Queries.CreateAgentPool(ctx,
		db.CreateAgentPoolParams{Name: "poll-pool"},
	)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := enrollment.repo.Queries.UpsertEnvironmentAgentPolicy(ctx,
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: environment.ID,
			PoolID:        pool.ID,
			Selector:      "",
		},
	); err != nil {
		t.Fatalf("set environment policy: %v", err)
	}
	return &pollFixture{
		enrollmentFixture: enrollment,
		poolID:            pool.ID,
		poolName:          pool.Name,
		releaseID:         release.ID,
		envID:             environment.ID,
	}
}

func (fixture *pollFixture) activate(
	t *testing.T,
	agentID string,
	identity agenttls.Identity,
) {
	t.Helper()
	fixture.createPendingAgent(t, agentID)
	fixture.createToken(
		t,
		agentID,
		agentID+"-token",
		fixture.now.AddDate(0, 0, 1),
	)
	response := fixture.enroll(t, agentID, identity, agentID+"-token")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("activate agent %q: status %d", agentID, response.StatusCode)
	}
}

func (fixture *pollFixture) addEligibleAgent(
	t *testing.T,
	agentID, selector string,
) {
	t.Helper()
	fixture.addPoolMember(t, fixture.poolName, agentID)
	if selector != "" {
		fixture.setTag(t, agentID, "region", selector[len("region="):])
	}
}

func (fixture *pollFixture) addPoolMember(
	t *testing.T,
	poolName, agentID string,
) {
	t.Helper()
	poolID := fixture.poolID
	if poolName != fixture.poolName {
		pool, err := fixture.repo.Queries.CreateAgentPool(
			context.Background(), db.CreateAgentPoolParams{Name: poolName},
		)
		if err != nil {
			t.Fatalf("create pool %q: %v", poolName, err)
		}
		poolID = pool.ID
	}
	if err := fixture.repo.Queries.AddAgentToPool(context.Background(),
		db.AddAgentToPoolParams{PoolID: poolID, AgentID: agentID},
	); err != nil {
		t.Fatalf("add agent %q to pool: %v", agentID, err)
	}
}

func (fixture *pollFixture) setTag(t *testing.T, agentID, key, value string) {
	t.Helper()
	if err := fixture.repo.Queries.SetAgentTag(context.Background(),
		db.SetAgentTagParams{AgentID: agentID, TagKey: key, TagValue: value},
	); err != nil {
		t.Fatalf("set agent tag: %v", err)
	}
}

func (fixture *pollFixture) createWaitingDeployment(
	t *testing.T,
	selector, payload string,
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
	if _, err := fixture.repo.Queries.CreateDeploymentDispatch(ctx,
		db.CreateDeploymentDispatchParams{
			DeploymentID: deployment.ID,
			Mode:         "remote",
			PoolID:       sql.NullInt64{Int64: fixture.poolID, Valid: true},
			Selector:     selector,
			State:        "waiting",
		},
	); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}
	return deployment.ID
}

func (fixture *pollFixture) createActiveClaim(t *testing.T, agentID string) {
	t.Helper()
	deploymentID := fixture.createWaitingDeployment(t, "", "active-payload")
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
