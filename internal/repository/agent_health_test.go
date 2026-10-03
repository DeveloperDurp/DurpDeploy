package repository_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestAgentHealthThresholds(t *testing.T) {
	// Given: an active agent with a heartbeat at a fixed time.
	agent := db.Agent{Status: "active", LastHeartbeatAt: ni(1000)}
	for _, test := range []struct {
		age  int64
		want string
	}{{119, "healthy"}, {120, "stale"}, {599, "stale"}, {600, "offline"}} {
		// When: health is evaluated at a threshold.
		got := repository.AgentHealthAt(agent, 1000+test.age, 1000)
		// Then: the boundary agrees with the fleet contract.
		if got != test.want {
			t.Fatalf("age=%d health=%s want=%s", test.age, got, test.want)
		}
	}
}

func TestAgentDrainAndHealthSurviveDatabaseRestart(t *testing.T) {
	// Given: durable maintenance and offline transition state.
	dsn := filepath.Join(t.TempDir(), "fleet.db")
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(conn)
	seedRemoteFixture(t, repo)
	if _, err := repo.SetAgentDraining(t.Context(), "a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AdvanceAgentHealth(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	// When: the server opens the database again.
	conn, err = migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	repo = repository.New(conn)

	// Then: drain remains set and offline transitions are not repeated.
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil || agent.Draining != 1 || agent.HealthState != "offline" {
		t.Fatalf("agent=%+v error=%v", agent, err)
	}
	transitions, err := repo.AdvanceAgentHealth(t.Context())
	if err != nil || len(transitions) != 0 {
		t.Fatalf("repeat transitions=%+v error=%v", transitions, err)
	}
	rows, err := repo.ClaimRemoteDeployment(
		t.Context(), currentClaimArg(t, repo),
	)
	if err != nil || rows != 0 {
		t.Fatalf("restart claim=%d error=%v", rows, err)
	}
}

func TestAgentHealthIncludesNeverReportedAndExcludesDisabled(t *testing.T) {
	// Given: an agent that never reported a heartbeat.
	agent := db.Agent{Status: "active", CreatedAt: 1000}
	for _, test := range []struct {
		now  int64
		want string
	}{{1119, "unknown"}, {1120, "stale"}, {1600, "offline"}} {
		// When: its initial heartbeat grace period expires.
		got := repository.AgentHealthAt(agent, test.now, 1000)
		// Then: it progresses to stale and offline.
		if got != test.want {
			t.Fatalf("health=%s want=%s", got, test.want)
		}
	}
	agent.Status = "disabled"
	agent.LastHeartbeatAt = sql.NullInt64{Int64: 1, Valid: true}
	if got := repository.AgentHealthAt(agent, 1600, 1000); got != "unknown" {
		t.Fatalf("disabled health=%s", got)
	}
}
