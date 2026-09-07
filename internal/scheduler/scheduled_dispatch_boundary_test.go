package scheduler_test

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/scheduler"
	"durpdeploy/internal/secret"
)

func TestScheduledOccurrenceCAS_PostCommitFailureLeavesOnlyIntent(t *testing.T) {
	// Given
	f := newFixture(t)
	project := f.createProject()
	release := f.createRelease(project.ID)
	environment := f.createEnvironment("post-commit")
	schedule := f.createSchedule(
		project.ID, release.ID, environment.ID, "* * * * *",
		f.now.Add(-time.Minute), 1, "post-commit",
	)
	label, err := f.repo.Queries.CreateAgentLabel(
		f.ctx(), db.CreateAgentLabelParams{
			Name: "Builders", NormalizedName: "builders",
		},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	createScheduledAgent(t, f, label.ID, "agent-a")
	if _, err := f.repo.Queries.CreateScheduledDeploymentRoutingPolicy(
		f.ctx(), db.CreateScheduledDeploymentRoutingPolicyParams{
			ScheduledDeploymentID: schedule.ID, TargetMode: "label",
			AgentLabelID: sql.NullInt64{Int64: label.ID, Valid: true},
			AgentStrategy: sql.NullString{
				String: "round_robin", Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("create schedule policy: %v", err)
	}
	creator := dispatch.NewCreationService(
		f.repo, dispatch.New(f.repo, nil, nil),
	)
	deployment, err := creator.CreateScheduled(
		f.ctx(), dispatch.ScheduledRequest{
			CreateRequest: dispatch.CreateRequest{
				ProjectID: project.ID, ReleaseID: release.ID,
				EnvironmentID: environment.ID,
			},
			ScheduleID: schedule.ID, DueAt: schedule.NextRunAt,
			NextRunAt: f.now.Add(time.Minute).Unix(),
		},
	)
	if err != nil {
		t.Fatalf("create occurrence intent: %v", err)
	}

	// When
	err = creator.DispatchFrozen(f.ctx(), deployment.ID)

	// Then
	if err == nil {
		t.Fatal("dispatch unexpectedly succeeded without a secret box")
	}
	assertNoScheduledRoutingArtifacts(t, f, deployment.ID, label.ID)
	recoverable, err := f.repo.Queries.ListRecoverableScheduledDeployments(f.ctx())
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != deployment.ID {
		t.Fatalf("recoverable roots = %#v, error = %v", recoverable, err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	recovery := dispatch.NewCreationService(
		f.repo, dispatch.New(f.repo, box, nil),
	)
	if err := recovery.DispatchFrozen(f.ctx(), deployment.ID); err != nil {
		t.Fatalf("recover dispatch: %v", err)
	}
	if _, err := f.repo.Queries.GetDeploymentRoutingSnapshot(
		f.ctx(), deployment.ID,
	); err != nil {
		t.Fatalf("get recovered snapshot: %v", err)
	}
	recoverable, err = f.repo.Queries.ListRecoverableScheduledDeployments(f.ctx())
	if err != nil || len(recoverable) != 0 {
		t.Fatalf("recoverable after success = %#v, error = %v", recoverable, err)
	}
}

func TestScheduledOccurrenceCAS_SeparateConnectionsCreateOneRoot(t *testing.T) {
	// Given
	f := newFixture(t)
	project := f.createProject()
	release := f.createRelease(project.ID)
	environment := f.createEnvironment("separate-connections")
	f.createSchedule(
		project.ID, release.ID, environment.ID, "* * * * *",
		f.now.Add(-time.Minute), 1, "separate-connections",
	)
	secondDB, err := sql.Open("sqlite", f.dsn)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	t.Cleanup(func() { _ = secondDB.Close() })
	secondRepo := repository.New(secondDB)
	secondScheduler := scheduler.New(
		secondRepo, dispatch.New(secondRepo, nil, nil),
		scheduler.WithNow(func() time.Time { return f.now }),
	)

	// When
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	for _, scheduled := range []*scheduler.Scheduler{f.sched, secondScheduler} {
		go func() {
			defer wait.Done()
			<-start
			scheduled.Tick(f.ctx())
		}()
	}
	close(start)
	wait.Wait()

	// Then
	deployments, err := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), release.ID)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("deployments = %#v, error = %v; want one", deployments, err)
	}
}

func assertNoScheduledRoutingArtifacts(
	t *testing.T,
	f *testFixture,
	deploymentID int64,
	labelID int64,
) {
	t.Helper()
	checks := []struct {
		name  string
		query string
		args  []any
	}{
		{"snapshot", "SELECT COUNT(*) FROM deployment_routing_snapshots WHERE deployment_id = ?", []any{deploymentID}},
		{"agents", "SELECT COUNT(*) FROM deployment_routing_agents WHERE deployment_id = ?", []any{deploymentID}},
		{"children", "SELECT COUNT(*) FROM deployments WHERE parent_deployment_id = ?", []any{deploymentID}},
		{"dispatch", "SELECT COUNT(*) FROM deployment_dispatches WHERE deployment_id = ?", []any{deploymentID}},
		{"cursor", "SELECT COUNT(*) FROM agent_label_cursors WHERE agent_label_id = ?", []any{labelID}},
	}
	for _, check := range checks {
		var count int
		if err := f.repo.DB.QueryRowContext(
			f.ctx(), check.query, check.args...,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want zero", check.name, count)
		}
	}
	if _, err := f.repo.Queries.GetDeploymentRoutingSnapshot(
		f.ctx(), deploymentID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("snapshot lookup error = %v, want no rows", err)
	}
}

func createScheduledAgent(
	t *testing.T,
	f *testFixture,
	labelID int64,
	id string,
) {
	t.Helper()
	if _, err := f.repo.DB.ExecContext(f.ctx(), `
INSERT INTO agents (
    id, name, status, certificate_pem, certificate_fingerprint
) VALUES (?, ?, 'active', 'certificate', ?)
`, id, id, fmt.Sprintf("%-64s", id)); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := f.repo.DB.ExecContext(f.ctx(), `
INSERT INTO agent_pairings (
    agent_id, pairing_code_hash, agent_public_identity, agent_pin,
    server_public_identity, server_pin, state, expires_at, paired_at
) VALUES (?, randomblob(32), ?, ?, 'server', ?, 'paired', 1, 1)
`, id, id, fmt.Sprintf("%-64s", id+"-pin"),
		fmt.Sprintf("%-64s", id+"-server")); err != nil {
		t.Fatalf("pair agent: %v", err)
	}
	if _, err := f.repo.Queries.CreateAgentLabelMembership(
		f.ctx(), db.CreateAgentLabelMembershipParams{
			AgentLabelID: labelID, AgentID: id,
		},
	); err != nil {
		t.Fatalf("label agent: %v", err)
	}
}
