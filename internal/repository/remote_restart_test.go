package repository_test

import (
	"path/filepath"
	"testing"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestRemoteLifecycleSurvivesRepositoryRestart(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "restart.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(conn)
	seedRemoteFixture(t, repo)
	claim := currentClaimArg(t, repo)
	changed, err := repo.ClaimRemoteDeployment(t.Context(), claim)
	assertOne(t, changed, err)
	if err := repo.StartRemoteDeployment(
		t.Context(),
		remoteIdentity(claim),
	); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Error(err)
		}
	})
	repo = repository.New(restarted)
	result, err := repo.FinishRemoteDeploymentLifecycle(
		t.Context(),
		remoteIdentity(claim),
		"succeeded",
	)
	if err != nil || !result.Changed {
		t.Fatalf("finish after restart result=%+v error=%v", result, err)
	}
	deployment, err := repo.Queries.GetDeployment(
		t.Context(),
		claim.DeploymentID,
	)
	if err != nil || deployment.Status != "succeeded" {
		t.Fatalf("deployment after restart=%+v error=%v", deployment, err)
	}
	claimRow, err := repo.Queries.GetRemoteDeploymentClaim(
		t.Context(),
		claim.DeploymentID,
	)
	if err != nil || claimRow.State != "succeeded" ||
		claimRow.AgentID != claim.AgentID {
		t.Fatalf("claim after restart=%+v error=%v", claimRow, err)
	}
}
