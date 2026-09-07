package scheduler_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/scheduler"
	"durpdeploy/internal/secret"
)

func TestScheduledExecutionTarget_LabelStrategiesDispatchExpectedSet(t *testing.T) {
	// Given
	f := newFixture(t)
	project := f.createProject()
	release := f.createRelease(project.ID)
	environment := f.createEnvironment("strategies")
	label, err := f.repo.Queries.CreateAgentLabel(
		f.ctx(), db.CreateAgentLabelParams{
			Name: "Builders", NormalizedName: "builders",
		},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	createScheduledAgent(t, f, label.ID, "agent-a")
	createScheduledAgent(t, f, label.ID, "agent-b")
	due := f.now.Add(-time.Minute)
	roundRobin := f.createSchedule(
		project.ID, release.ID, environment.ID, "* * * * *",
		due, 1, "round-robin",
	)
	all := f.createSchedule(
		project.ID, release.ID, environment.ID, "* * * * *",
		due, 1, "all",
	)
	for _, policy := range []db.CreateScheduledDeploymentRoutingPolicyParams{
		{
			ScheduledDeploymentID: roundRobin.ID, TargetMode: "label",
			AgentLabelID: sql.NullInt64{Int64: label.ID, Valid: true},
			AgentStrategy: sql.NullString{
				String: "round_robin", Valid: true,
			},
		},
		{
			ScheduledDeploymentID: all.ID, TargetMode: "label",
			AgentLabelID:  sql.NullInt64{Int64: label.ID, Valid: true},
			AgentStrategy: sql.NullString{String: "all", Valid: true},
		},
	} {
		if _, err := f.repo.Queries.CreateScheduledDeploymentRoutingPolicy(
			f.ctx(), policy,
		); err != nil {
			t.Fatalf("create schedule policy: %v", err)
		}
	}
	f.sched = scheduledTestDispatcher(t, f)

	// When
	f.sched.Tick(f.ctx())

	// Then
	roundRobinOccurrence, err := f.repo.Queries.GetScheduledDeploymentOccurrence(
		f.ctx(), db.GetScheduledDeploymentOccurrenceParams{
			ScheduledDeploymentID: roundRobin.ID, DueAt: due.Unix(),
		},
	)
	if err != nil {
		t.Fatalf("get round-robin occurrence: %v", err)
	}
	selected, err := f.repo.Queries.ListDeploymentRoutingAgents(
		f.ctx(), roundRobinOccurrence.DeploymentID,
	)
	if err != nil || len(selected) != 1 {
		t.Fatalf("round-robin agents = %#v, error = %v", selected, err)
	}
	allOccurrence, err := f.repo.Queries.GetScheduledDeploymentOccurrence(
		f.ctx(), db.GetScheduledDeploymentOccurrenceParams{
			ScheduledDeploymentID: all.ID, DueAt: due.Unix(),
		},
	)
	if err != nil {
		t.Fatalf("get all occurrence: %v", err)
	}
	children, err := f.repo.Queries.ListDeploymentChildren(
		f.ctx(), sql.NullInt64{Int64: allOccurrence.DeploymentID, Valid: true},
	)
	if err != nil || len(children) != 2 {
		t.Fatalf("all children = %#v, error = %v", children, err)
	}
}

func TestScheduledExecutionTarget_NoMembersFailsOneRootAndAdvances(t *testing.T) {
	// Given
	f := newFixture(t)
	project := f.createProject()
	release := f.createRelease(project.ID)
	environment := f.createEnvironment("empty")
	label, err := f.repo.Queries.CreateAgentLabel(
		f.ctx(), db.CreateAgentLabelParams{Name: "Empty", NormalizedName: "empty"},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	due := f.now.Add(-time.Minute)
	schedule := f.createSchedule(
		project.ID, release.ID, environment.ID, "* * * * *",
		due, 1, "empty",
	)
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

	// When
	f.sched.Tick(f.ctx())
	f.sched.Tick(f.ctx())

	// Then
	deployments, err := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), release.ID)
	if err != nil || len(deployments) != 1 || deployments[0].Status != "failed" {
		t.Fatalf("deployments = %#v, error = %v", deployments, err)
	}
	updated, err := f.repo.Queries.GetScheduledDeployment(f.ctx(), schedule.ID)
	if err != nil || !updated.LastFiredAt.Valid ||
		updated.LastFiredAt.Int64 != due.Unix() || updated.NextRunAt <= f.now.Unix() {
		t.Fatalf("advanced schedule = %#v, error = %v", updated, err)
	}
	if _, err := f.repo.Queries.GetAgentLabelCursor(
		f.ctx(), label.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("round-robin cursor error = %v, want no cursor", err)
	}
}

func TestScheduledApprovalRouting_EmptyAtFireCanApproveLater(t *testing.T) {
	// Given
	f, release, schedule, label := scheduledApprovalFixture(t)
	f.sched.Tick(f.ctx())
	occurrence, err := f.repo.Queries.GetScheduledDeploymentOccurrence(
		f.ctx(), db.GetScheduledDeploymentOccurrenceParams{
			ScheduledDeploymentID: schedule.ID, DueAt: schedule.NextRunAt,
		},
	)
	if err != nil {
		t.Fatalf("get occurrence: %v", err)
	}
	root, err := f.repo.Queries.GetDeployment(f.ctx(), occurrence.DeploymentID)
	if err != nil || root.Status != "pending_approval" {
		t.Fatalf("root = %#v, error = %v", root, err)
	}
	assertNoScheduledRoutingArtifacts(t, f, root.ID, label.ID)
	createScheduledAgent(t, f, label.ID, "agent-later")
	service := dispatch.NewCreationService(
		f.repo, scheduledDispatcher(t, f),
	)

	// When
	err = service.Approve(f.ctx(), root.ID, dispatch.Approval{ApprovedBy: "qa"})

	// Then
	if err != nil {
		t.Fatalf("approve after member joined: %v", err)
	}
	agents, err := f.repo.Queries.ListDeploymentRoutingAgents(f.ctx(), root.ID)
	if err != nil || len(agents) != 1 || agents[0].AgentID != "agent-later" {
		t.Fatalf("agents = %#v, error = %v", agents, err)
	}
	deployments, err := f.repo.Queries.ListDeploymentsByRelease(f.ctx(), release.ID)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("roots = %#v, error = %v", deployments, err)
	}
}

func TestScheduledApprovalRouting_EligibilityLossKeepsPendingIntent(t *testing.T) {
	// Given
	f, _, schedule, label := scheduledApprovalFixture(t)
	createScheduledAgent(t, f, label.ID, "agent-lost")
	f.sched.Tick(f.ctx())
	occurrence, err := f.repo.Queries.GetScheduledDeploymentOccurrence(
		f.ctx(), db.GetScheduledDeploymentOccurrenceParams{
			ScheduledDeploymentID: schedule.ID, DueAt: schedule.NextRunAt,
		},
	)
	if err != nil {
		t.Fatalf("get occurrence: %v", err)
	}
	if _, err := f.repo.DB.ExecContext(
		f.ctx(), "UPDATE agents SET status = 'disabled' WHERE id = 'agent-lost'",
	); err != nil {
		t.Fatalf("disable member: %v", err)
	}
	service := dispatch.NewCreationService(
		f.repo, scheduledDispatcher(t, f),
	)

	// When
	err = service.Approve(
		f.ctx(), occurrence.DeploymentID, dispatch.Approval{ApprovedBy: "qa"},
	)

	// Then
	if !errors.Is(err, dispatch.ErrNoEligibleAgents) {
		t.Fatalf("approve error = %v, want no eligible agents", err)
	}
	root, getErr := f.repo.Queries.GetDeployment(f.ctx(), occurrence.DeploymentID)
	if getErr != nil || root.Status != "pending_approval" {
		t.Fatalf("root = %#v, error = %v", root, getErr)
	}
	assertNoScheduledRoutingArtifacts(t, f, root.ID, label.ID)
}

func scheduledApprovalFixture(
	t *testing.T,
) (*testFixture, db.Release, db.ScheduledDeployment, db.AgentLabel) {
	t.Helper()
	f := newFixture(t)
	project := f.createProject()
	release := f.createRelease(project.ID)
	environment := f.createEnvironment("approval-label")
	lifecycle := f.createLifecycle("approval-label")
	f.attachLifecycle(project.ID, lifecycle.ID)
	f.addLifecycleStageWithApproval(lifecycle.ID, environment.ID, 0)
	label, err := f.repo.Queries.CreateAgentLabel(
		f.ctx(), db.CreateAgentLabelParams{
			Name: "Approval", NormalizedName: "approval",
		},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	schedule := f.createSchedule(
		project.ID, release.ID, environment.ID, "* * * * *",
		f.now.Add(-time.Minute), 1, "approval-label",
	)
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
	return f, release, schedule, label
}

func scheduledTestDispatcher(t *testing.T, f *testFixture) *scheduler.Scheduler {
	t.Helper()
	return scheduler.New(
		f.repo, scheduledDispatcher(t, f),
		scheduler.WithNow(func() time.Time { return f.now }),
	)
}

func scheduledDispatcher(t *testing.T, f *testFixture) *dispatch.Dispatcher {
	t.Helper()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	return dispatch.New(f.repo, box, nil)
}
