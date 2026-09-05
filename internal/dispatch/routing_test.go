package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestRoutingSnapshot_LabelHasNoEligibleAgentsDoesNotFallback(t *testing.T) {
	fixture := newRoutingFixture(t, 0)
	policy := Policy{
		Source: SourceRequest, Mode: TargetLabel, LabelID: fixture.label.ID,
		LabelName: fixture.label.Name, Strategy: StrategyRoundRobin,
	}
	deployment := fixture.createDeployment(t)

	err := NewResolver(fixture.repo).Freeze(
		context.Background(),
		deployment.ID,
		policy,
	)
	if !errors.Is(err, ErrNoEligibleAgents) {
		t.Fatalf("Freeze() error = %v, want %v", err, ErrNoEligibleAgents)
	}
	_, err = fixture.repo.Queries.GetDeploymentRoutingSnapshot(
		context.Background(), deployment.ID,
	)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("snapshot error = %v, want rollback", err)
	}
}

func TestRoutingSnapshot_DispatchSelectsFrozenRoundRobinTarget(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	deployment := fixture.createDeployment(t)
	ctx := context.Background()
	_, err := fixture.repo.Queries.CreateDeploymentRoutingSnapshot(
		ctx,
		db.CreateDeploymentRoutingSnapshotParams{
			DeploymentID: deployment.ID, Source: "request", TargetMode: "label",
			AgentLabelID: sql.NullInt64{Int64: fixture.label.ID, Valid: true},
			AgentLabelName: sql.NullString{
				String: fixture.label.Name,
				Valid:  true,
			},
			AgentStrategy: sql.NullString{String: "round_robin", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create routing intent: %v", err)
	}
	_, box := newPayloadRepository(t)
	if err := New(fixture.repo, box, nil).Dispatch(ctx, deployment.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	dispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
		ctx,
		deployment.ID,
	)
	if err != nil {
		t.Fatalf("get deployment dispatch: %v", err)
	}
	if dispatch.Mode != "remote" ||
		dispatch.AssignedAgentID.String != "agent-a" {
		t.Fatalf("dispatch = %#v", dispatch)
	}
}

func TestRoutingSnapshot_ExplicitLocalOverridesLegacyAssignment(t *testing.T) {
	fixture := newRoutingFixture(t, 1)
	deployment := fixture.createDeployment(t)
	resolver := NewResolver(fixture.repo)
	if err := resolver.Freeze(context.Background(), deployment.ID, Policy{
		Source: SourceRequest, Mode: TargetLocal,
	}); err != nil {
		t.Fatalf("freeze local routing: %v", err)
	}
	_, box := newPayloadRepository(t)
	if err := New(fixture.repo, box, nil).Dispatch(
		context.Background(), deployment.ID,
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	dispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(), deployment.ID,
	)
	if err != nil || dispatch.Mode != "local" {
		t.Fatalf("dispatch = %#v, %v", dispatch, err)
	}
}

func TestRoutingSnapshot_PendingApprovalDefersEligibleSelection(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	deployment := fixture.createDeployment(t)
	if _, err := fixture.repo.Queries.UpdateDeployment(
		context.Background(),
		db.UpdateDeploymentParams{
			ID: deployment.ID, ReleaseID: deployment.ReleaseID,
			EnvironmentID: deployment.EnvironmentID,
			Status:        "pending_approval",
		},
	); err != nil {
		t.Fatalf("make deployment pending approval: %v", err)
	}
	_, err := fixture.repo.Queries.CreateDeploymentRoutingSnapshot(
		context.Background(),
		db.CreateDeploymentRoutingSnapshotParams{
			DeploymentID: deployment.ID, Source: "request", TargetMode: "label",
			AgentLabelID: sql.NullInt64{Int64: fixture.label.ID, Valid: true},
			AgentLabelName: sql.NullString{
				String: fixture.label.Name, Valid: true,
			},
			AgentStrategy: sql.NullString{String: "all", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create routing intent: %v", err)
	}
	_, box := newPayloadRepository(t)
	if err := New(fixture.repo, box, nil).Dispatch(
		context.Background(), deployment.ID,
	); err != nil {
		t.Fatalf("dispatch pending approval: %v", err)
	}
	agents, err := NewResolver(fixture.repo).SelectedAgents(
		context.Background(), deployment.ID,
	)
	if err != nil || len(agents) != 0 {
		t.Fatalf("pending approval selected agents = %#v, %v", agents, err)
	}
}

func TestRoutingSnapshot_MembershipAndStatusMutationCannotChangeSnapshot(
	t *testing.T,
) {
	fixture := newRoutingFixture(t, 3)
	deployment := fixture.createDeployment(t)
	policy := Policy{
		Source: SourceRequest, Mode: TargetLabel, LabelID: fixture.label.ID,
		LabelName: fixture.label.Name, Strategy: StrategyAll,
	}
	resolver := NewResolver(fixture.repo)
	if err := resolver.Freeze(context.Background(), deployment.ID, policy); err != nil {
		t.Fatalf("freeze routing: %v", err)
	}
	if _, err := fixture.repo.Queries.DeleteAgentLabelMembership(
		context.Background(),
		db.DeleteAgentLabelMembershipParams{
			AgentLabelID: fixture.label.ID, AgentID: "agent-a",
		},
	); err != nil {
		t.Fatalf("delete membership: %v", err)
	}
	if _, err := fixture.repo.Queries.DisableAgent(
		context.Background(),
		db.DisableAgentParams{ID: "agent-b", UpdatedAt: 2},
	); err != nil {
		t.Fatalf("disable agent: %v", err)
	}
	if _, err := fixture.repo.Queries.RevokeAgent(
		context.Background(),
		db.RevokeAgentParams{
			ID: "agent-c", UpdatedAt: 2,
			RevokedAt: sql.NullInt64{Int64: 2, Valid: true},
		},
	); err != nil {
		t.Fatalf("revoke agent: %v", err)
	}
	if _, err := fixture.repo.Queries.UpdateAgentLabel(
		context.Background(),
		db.UpdateAgentLabelParams{
			ID: fixture.label.ID, Name: "Renamed", NormalizedName: "renamed",
		},
	); err != nil {
		t.Fatalf("rename label: %v", err)
	}
	agents, err := resolver.SelectedAgents(context.Background(), deployment.ID)
	if err != nil {
		t.Fatalf("selected agents: %v", err)
	}
	if got := agentIDs(agents); fmt.Sprint(got) != "[agent-a agent-b agent-c]" {
		t.Fatalf("selected agents = %v", got)
	}
	snapshot, err := fixture.repo.Queries.GetDeploymentRoutingSnapshot(
		context.Background(), deployment.ID,
	)
	if err != nil || snapshot.AgentLabelName.String != "Builders" {
		t.Fatalf("routing snapshot = %#v, %v", snapshot, err)
	}
	t.Logf(
		"after membership delete, disable, revoke, and label rename: selected=%v label=%q",
		agentIDs(agents),
		snapshot.AgentLabelName.String,
	)
}

func TestRoutingSnapshot_ExcludesDisabledAndRevokedButNotStaleHeartbeat(
	t *testing.T,
) {
	fixture := newRoutingFixture(t, 3)
	ctx := context.Background()
	if _, err := fixture.repo.Queries.DisableAgent(
		ctx,
		db.DisableAgentParams{ID: "agent-b", UpdatedAt: 2},
	); err != nil {
		t.Fatalf("disable agent: %v", err)
	}
	if _, err := fixture.repo.Queries.RevokeAgent(
		ctx,
		db.RevokeAgentParams{
			ID: "agent-c", UpdatedAt: 2,
			RevokedAt: sql.NullInt64{Int64: 2, Valid: true},
		},
	); err != nil {
		t.Fatalf("revoke agent: %v", err)
	}
	deployment := fixture.createDeployment(t)
	resolver := NewResolver(fixture.repo)
	if err := resolver.Freeze(ctx, deployment.ID, Policy{
		Source: SourceRequest, Mode: TargetLabel, LabelID: fixture.label.ID,
		LabelName: fixture.label.Name, Strategy: StrategyAll,
	}); err != nil {
		t.Fatalf("freeze routing: %v", err)
	}
	agents, err := resolver.SelectedAgents(ctx, deployment.ID)
	if err != nil || len(agents) != 1 || agents[0].ID != "agent-a" {
		t.Fatalf("eligible agents = %#v, %v", agents, err)
	}
}
