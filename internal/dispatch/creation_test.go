package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentExecutionTarget_CreatesFrozenLabelRouting(t *testing.T) {
	// Given
	fixture := newRoutingFixture(t, 2)
	service := NewCreationService(
		fixture.repo,
		New(fixture.repo, fixture.box, nil),
	)

	// When
	deployment, err := service.Create(context.Background(), CreateRequest{
		ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing: Input{
			Source: SourceRequest, Mode: "label", LabelID: fixture.label.ID,
			Strategy: "all",
		},
	})
	// Then
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	snapshot, err := fixture.repo.Queries.GetDeploymentRoutingSnapshot(
		context.Background(), deployment.ID,
	)
	if err != nil {
		t.Fatalf("get routing snapshot: %v", err)
	}
	if snapshot.Source != "request" || snapshot.TargetMode != "label" ||
		snapshot.AgentLabelID.Int64 != fixture.label.ID ||
		snapshot.AgentStrategy.String != "all" {
		t.Fatalf("routing snapshot = %#v", snapshot)
	}
	children, err := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(),
		sql.NullInt64{Int64: deployment.ID, Valid: true},
	)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
}

func TestDeploymentRoutingAtomicity_EmptyImmediateLabelCreatesNoRoot(
	t *testing.T,
) {
	// Given
	fixture := newRoutingFixture(t, 0)
	service := NewCreationService(
		fixture.repo,
		New(fixture.repo, fixture.box, nil),
	)

	// When
	_, err := service.Create(context.Background(), CreateRequest{
		ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing: Input{
			Source: SourceRequest, Mode: "label", LabelID: fixture.label.ID,
			Strategy: "round_robin",
		},
	})

	// Then
	if !errors.Is(err, ErrNoEligibleAgents) {
		t.Fatalf("Create() error = %v, want %v", err, ErrNoEligibleAgents)
	}
	deployments, listErr := fixture.repo.Queries.ListDeployments(
		context.Background(),
	)
	if listErr != nil {
		t.Fatalf("list deployments: %v", listErr)
	}
	if len(deployments) != 0 {
		t.Fatalf("deployments = %d, want 0", len(deployments))
	}
}

func TestDeploymentApprovalRouting_SelectsMembersAtApproval(t *testing.T) {
	// Given
	fixture := newRoutingFixture(t, 0)
	service := NewCreationService(
		fixture.repo,
		New(fixture.repo, fixture.box, nil),
	)
	requireApprovalForFixture(t, fixture)
	deployment, err := service.Create(context.Background(), CreateRequest{
		ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing: Input{
			Source: SourceRequest, Mode: "label", LabelID: fixture.label.ID,
			Strategy: "round_robin",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	createEligibleAgent(t, fixture.repo, fixture.label.ID, "agent-a")

	// When
	err = service.Approve(context.Background(), deployment.ID, Approval{
		ApprovedBy: "admin",
	})
	// Then
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	updated, err := fixture.repo.Queries.GetDeployment(
		context.Background(), deployment.ID,
	)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.Status != "pending" {
		t.Fatalf("status = %q, want pending", updated.Status)
	}
	agents, err := fixture.repo.Queries.ListDeploymentRoutingAgents(
		context.Background(), deployment.ID,
	)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 1 || agents[0].AgentID != "agent-a" {
		t.Fatalf("agents = %#v", agents)
	}
}

func TestDeploymentApprovalRouting_EligibilityLossLeavesPendingRoot(
	t *testing.T,
) {
	// Given
	fixture := newRoutingFixture(t, 1)
	service := NewCreationService(
		fixture.repo,
		New(fixture.repo, fixture.box, nil),
	)
	requireApprovalForFixture(t, fixture)
	deployment, err := service.Create(context.Background(), CreateRequest{
		ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing: Input{
			Source: SourceRequest, Mode: "label", LabelID: fixture.label.ID,
			Strategy: "all",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := fixture.repo.DB.ExecContext(
		context.Background(),
		"UPDATE agents SET status = 'disabled' WHERE id = 'agent-a'",
	); err != nil {
		t.Fatalf("disable agent: %v", err)
	}

	// When
	err = service.Approve(context.Background(), deployment.ID, Approval{
		ApprovedBy: "admin",
	})

	// Then
	if !errors.Is(err, ErrNoEligibleAgents) {
		t.Fatalf("Approve() error = %v, want %v", err, ErrNoEligibleAgents)
	}
	updated, getErr := fixture.repo.Queries.GetDeployment(
		context.Background(), deployment.ID,
	)
	if getErr != nil {
		t.Fatalf("get deployment: %v", getErr)
	}
	if updated.Status != "pending_approval" {
		t.Fatalf("status = %q, want pending_approval", updated.Status)
	}
	if _, getErr = fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(), deployment.ID,
	); !errors.Is(getErr, sql.ErrNoRows) {
		t.Fatalf("dispatch error = %v, want sql.ErrNoRows", getErr)
	}
}

func TestDeploymentApprovalRouting_FreezesLegacyAgentAtRequest(t *testing.T) {
	fixture := newRoutingFixture(t, 2)
	requireApprovalForFixture(t, fixture)
	ctx := context.Background()
	service := NewCreationService(
		fixture.repo,
		New(fixture.repo, fixture.box, nil),
	)
	deployment, err := service.Create(ctx, CreateRequest{
		ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing:       Input{Source: SourceRequest, Mode: "default"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := fixture.repo.DB.ExecContext(
		ctx,
		"UPDATE environment_agent_assignments SET agent_id = 'agent-b'"+
			" WHERE environment_id = ?",
		fixture.environment.ID,
	); err != nil {
		t.Fatalf("change assignment: %v", err)
	}
	if err := service.Approve(ctx, deployment.ID, Approval{
		ApprovedBy: "admin",
	}); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	dispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
		ctx, deployment.ID,
	)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if dispatch.AssignedAgentID.String != "agent-a" {
		t.Fatalf(
			"assigned agent = %q, want frozen agent-a",
			dispatch.AssignedAgentID.String,
		)
	}
}

func requireApprovalForFixture(t *testing.T, fixture routingFixture) {
	t.Helper()
	ctx := context.Background()
	lifecycle, err := fixture.repo.Queries.CreateLifecycle(
		ctx, db.CreateLifecycleParams{Name: "approval"},
	)
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	if err := fixture.repo.Queries.SetProjectLifecycle(
		ctx,
		db.SetProjectLifecycleParams{
			ID: fixture.project.ID,
			LifecycleID: sql.NullInt64{
				Int64: lifecycle.ID, Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("set lifecycle: %v", err)
	}
	if _, err := fixture.repo.Queries.CreateLifecycleStage(
		ctx,
		db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: fixture.environment.ID,
			SortOrder: 0, RequiresApproval: 1,
		},
	); err != nil {
		t.Fatalf("create lifecycle stage: %v", err)
	}
}
