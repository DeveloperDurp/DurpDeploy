package db_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

func TestRemoteAgentQueriesSmoke(t *testing.T) {
	// Given
	ctx := context.Background()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	queries := db.New(conn)

	project, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "remote-project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "remote-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "remote-v1",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: environment.ID,
		Status:        "pending",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	pool, err := queries.CreateAgentPool(ctx, db.CreateAgentPoolParams{
		Name: "remote-pool",
	})
	if err != nil {
		t.Fatalf("create agent pool: %v", err)
	}
	agent, err := queries.CreatePendingAgent(ctx, db.CreatePendingAgentParams{
		ID:           "remote-agent",
		Name:         "remote-agent",
		AgentVersion: sql.NullString{String: "v1", Valid: true},
	})
	if err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	if _, err := queries.ActivatePendingAgent(
		ctx,
		db.ActivatePendingAgentParams{
			CertificatePem: sql.NullString{String: "certificate", Valid: true},
			CertificateFingerprint: sql.NullString{
				String: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Valid:  true,
			},
			AgentVersion:    agent.AgentVersion,
			LastHeartbeatAt: sql.NullInt64{Int64: 1, Valid: true},
			EnrolledAt:      sql.NullInt64{Int64: 1, Valid: true},
			UpdatedAt:       1,
			ID:              agent.ID,
		},
	); err != nil {
		t.Fatalf("activate pending agent: %v", err)
	}
	if err := queries.AddAgentToPool(ctx, db.AddAgentToPoolParams{
		PoolID:  pool.ID,
		AgentID: agent.ID,
	}); err != nil {
		t.Fatalf("add agent to pool: %v", err)
	}
	if err := queries.SetAgentTag(ctx, db.SetAgentTagParams{
		AgentID:  agent.ID,
		TagKey:   "region",
		TagValue: "us",
	}); err != nil {
		t.Fatalf("set agent tag: %v", err)
	}
	if err := queries.UpsertEnvironmentAgentPolicy(
		ctx,
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: environment.ID,
			PoolID:        pool.ID,
			Selector:      "region=us",
		},
	); err != nil {
		t.Fatalf("upsert environment agent policy: %v", err)
	}

	// When
	candidates, err := queries.ListAgentPoolCandidatesByEnvironment(
		ctx,
		environment.ID,
	)
	if err != nil {
		t.Fatalf("list agent pool candidates: %v", err)
	}
	dispatch, err := queries.CreateDeploymentDispatch(
		ctx,
		db.CreateDeploymentDispatchParams{
			DeploymentID: deployment.ID,
			Mode:         "remote",
			PoolID:       sql.NullInt64{Int64: pool.ID, Valid: true},
			Selector:     "region=us",
			State:        "waiting",
		},
	)
	if err != nil {
		t.Fatalf("create deployment dispatch: %v", err)
	}
	claimed, err := queries.ClaimDeploymentDispatch(
		ctx,
		db.ClaimDeploymentDispatchParams{
			AgentID:         sql.NullString{String: agent.ID, Valid: true},
			ClaimTokenHash:  bytes.Repeat([]byte{1}, 32),
			ClaimExpiresAt:  sql.NullInt64{Int64: 2, Valid: true},
			LastHeartbeatAt: sql.NullInt64{Int64: 1, Valid: true},
			DeploymentID:    dispatch.DeploymentID,
		},
	)
	if err != nil {
		t.Fatalf("claim deployment dispatch: %v", err)
	}

	// Then
	if len(candidates) != 1 || candidates[0].ID != agent.ID {
		t.Fatalf("candidates = %#v, want active pool member", candidates)
	}
	if claimed.State != "claimed" || claimed.AgentID.String != agent.ID {
		t.Fatalf(
			"claimed dispatch = %#v, want claimed by %q",
			claimed,
			agent.ID,
		)
	}
}
