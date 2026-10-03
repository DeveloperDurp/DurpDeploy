package repository_test

import (
	"reflect"
	"testing"

	"durpdeploy/internal/repository"
)

func runAgentFleetDatabaseParity(t *testing.T, repo *repository.Repository) {
	t.Helper()
	if changed, err := repo.SetAgentDraining(
		t.Context(),
		"a",
		true,
	); err != nil ||
		!changed {
		t.Fatalf("drain=%v err=%v", changed, err)
	}
	if rows, err := repo.ClaimRemoteDeployment(
		t.Context(),
		currentClaimArg(t, repo),
	); err != nil ||
		rows != 0 {
		t.Fatalf("draining claim rows=%d err=%v", rows, err)
	}
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	report, err := repo.AgentHealthReport(t.Context(), agent)
	if err != nil || report.QueuedWork != 2 || len(report.CurrentWork) != 0 ||
		report.Health != "offline" {
		t.Fatalf("health report=%+v err=%v", report, err)
	}
	agents, err := repo.Queries.ListAgents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fleet, _, err := repo.AgentFleetReports(t.Context(), agents)
	if err != nil || !reflect.DeepEqual(fleet["a"], report) {
		t.Fatalf("fleet/detail parity=%+v err=%v", fleet, err)
	}
	if transitions, err := repo.AdvanceAgentHealth(
		t.Context(),
	); err != nil ||
		len(transitions) != 2 {
		t.Fatalf("health transitions=%+v err=%v", transitions, err)
	}
	if transitions, err := repo.AdvanceAgentHealth(
		t.Context(),
	); err != nil ||
		len(transitions) != 0 {
		t.Fatalf("repeated health transitions=%+v err=%v", transitions, err)
	}
	if changed, err := repo.SetAgentDraining(
		t.Context(),
		"a",
		false,
	); err != nil ||
		!changed {
		t.Fatalf("resume=%v err=%v", changed, err)
	}
}
