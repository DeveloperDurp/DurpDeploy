package repository_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func ni(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

func ns(
	v string,
) sql.NullString {
	return sql.NullString{String: v, Valid: true}
}

func remoteFixture(t *testing.T) *repository.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote.db")
	conn, err := migrate.Run(path +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		t.Log(
			"cleanup: fixture connection closed; t.TempDir removes database/WAL/SHM",
		)
	})
	r := repository.New(conn)
	seedRemoteFixture(t, r)
	return r
}

func seedRemoteFixture(t *testing.T, r *repository.Repository) {
	t.Helper()
	ctx := context.Background()
	_, err := r.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "remote"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if _, err := r.Queries.CreateEnvironment(ctx,
			db.CreateEnvironmentParams{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = r.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: 1, Version: "v1", StepsJson: `[{"name":"frozen"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a", "b"} {
		_, err := r.Queries.CreateAgent(ctx, db.CreateAgentParams{
			ID: id, Name: id, Endpoint: "https://fixture.invalid",
		})
		if err != nil {
			t.Fatal(err)
		}
		code := bytes.Repeat([]byte{byte(i + 1)}, 32)
		pin := strings.Repeat(id, 64)
		_, err = r.Queries.CreateAgentPairing(ctx, db.CreateAgentPairingParams{
			AgentID: id, PairingCodeHash: code, AgentPublicIdentity: "public",
			AgentPin: pin, ExpiresAt: 500,
		})
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.Queries.BeginPairingCommit(ctx, db.BeginPairingCommitParams{
			AgentID: id, PairingCodeHash: code, Now: 100,
			ServerPublicIdentity: ns("public"), ServerPin: ns(pin),
			EncryptedIdentity: ns("fixture-ciphertext"),
		})
		assertOne(t, n, err)
		n, err = r.CommitAgentPairing(ctx, db.CompleteAgentPairingParams{
			AgentID: id, Now: ni(100), ServerPin: ns(pin),
		}, db.ActivatePairedAgentParams{
			CertificatePem:         ns("fixture-public-certificate"),
			CertificateFingerprint: ns(pin),
		})
		assertOne(t, n, err)
		n, err = r.Queries.HeartbeatAgent(ctx, db.HeartbeatAgentParams{
			ID: id, Now: ni(100), CertificateFingerprint: ns(pin),
		})
		assertOne(t, n, err)
		if id == "a" {
			n, err = r.Queries.AssignEnvironmentAgent(ctx,
				db.AssignEnvironmentAgentParams{EnvironmentID: 1, AgentID: id})
			assertOne(t, n, err)
		}
		n, err = r.Queries.AddAgentLabel(ctx,
			db.AddAgentLabelParams{AgentID: id, Label: "linux"})
		assertOne(t, n, err)
	}
	for _, env := range []int64{1, 1, 2} {
		assignedAgentID := ns("a")
		if env == 2 {
			assignedAgentID = sql.NullString{}
		}
		if _, err := r.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: env, Status: "pending",
			AssignedAgentID: assignedAgentID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, deploymentID := range []int64{1, 2} {
		n, err := r.Queries.CreateRemoteDeploymentClaim(ctx, deploymentID)
		assertOne(t, n, err)
	}
	for _, id := range []int64{1, 2, 3} {
		err := r.SnapshotDeploymentSteps(
			ctx,
			id,
			[]repository.DeploymentStepSnapshot{
				{CreateDeploymentStepParams: db.CreateDeploymentStepParams{
					Name: "remote", ScriptBody: "echo remote", ExecutionTarget: "agent", MaxRetries: 1,
				}, Selectors: []string{" LINUX ", "linux", "other"}},
				{CreateDeploymentStepParams: db.CreateDeploymentStepParams{
					Name: "next", ScriptBody: "echo next", ExecutionTarget: "local",
				}},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.Queries.CreateDeploymentStepAttempt(ctx,
			db.CreateDeploymentStepAttemptParams{
				DeploymentID: id, StepIndex: 0, Attempt: 1, WaitDeadline: 400, Now: 100,
			})
		if err != nil || n != 1 {
			t.Fatalf("CreateDeploymentStepAttempt rows=%d error=%v", n, err)
		}
	}
	t.Log(
		"baseline rows: agents=2 paired=2 assignments=1 claims=2 steps=6 attempts=3; cursor=0",
	)
}

func claimArg(agent string) db.ClaimRemoteDeploymentParams {
	return db.ClaimRemoteDeploymentParams{
		DeploymentID: 1, AgentID: agent,
		ClaimTokenHash: bytes.Repeat([]byte(agent), 32),
		Ciphertext:     ns("ciphertext"), Now: 100, ClaimExpiresAt: 110,
	}
}

func startArg(
	arg db.ClaimRemoteDeploymentParams,
) db.StartRemoteDeploymentParams {
	return db.StartRemoteDeploymentParams{
		DeploymentID: arg.DeploymentID, AgentID: arg.AgentID,
		ClaimTokenHash: arg.ClaimTokenHash, Now: ni(arg.Now),
	}
}

func assertOne(t *testing.T, n int64, err error) {
	t.Helper()
	if err != nil || n != 1 {
		t.Fatalf("want one affected row; rows=%d err=%v", n, err)
	}
}

func assertZero(t *testing.T, n int64, err error) {
	t.Helper()
	if err != nil || n != 0 {
		t.Fatalf("want zero affected rows; rows=%d err=%v", n, err)
	}
}
