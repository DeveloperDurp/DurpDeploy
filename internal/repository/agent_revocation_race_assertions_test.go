package repository

import (
	"testing"
)

func assertRevocationRaceOutcome(
	t *testing.T,
	repo *Repository,
	operation, order string,
	fixture revocationRaceFixture,
) {
	t.Helper()
	agent, err := repo.Queries.GetAgent(t.Context(), fixture.agentID)
	if err != nil || agent.Status != "revoked" {
		t.Fatalf("agent=%+v err=%v", agent, err)
	}
	claim, err := repo.Queries.GetRemoteDeploymentClaim(
		t.Context(),
		fixture.deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.GetDeployment(
		t.Context(),
		fixture.deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if assertRevocationTerminalState(
		t,
		operation,
		claim.State,
		deployment.Status,
	) {
		return
	}
	if deployment.Status != "failed" ||
		(claim.State != "failed" && claim.State != "lost") {
		t.Fatalf("claim=%s deployment=%s", claim.State, deployment.Status)
	}
	if operation == "Logs" && order == "LogsFirst" && claim.State == "lost" {
		var count int
		if err := repo.DB.QueryRowContext(
			t.Context(),
			"SELECT count(*) FROM deployment_logs WHERE deployment_id = ?",
			fixture.deploymentID,
		).Scan(&count); err != nil || count != 1 {
			t.Fatalf("logs=%d err=%v", count, err)
		}
	}
}

func assertRevocationTerminalState(
	t *testing.T,
	operation, claimState, deploymentStatus string,
) bool {
	t.Helper()
	if operation == "Result" && claimState == "succeeded" {
		if deploymentStatus != "succeeded" {
			t.Fatalf("deployment=%s", deploymentStatus)
		}
		return true
	}
	if operation == "Cancelled" && claimState == "cancelled" {
		if deploymentStatus != "cancelled" {
			t.Fatalf("deployment=%s", deploymentStatus)
		}
		return true
	}
	return false
}
