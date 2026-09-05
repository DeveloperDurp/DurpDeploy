package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/deploymentstate"
)

func TestFanout_CreatesAtomicOrdinaryChildren(t *testing.T) {
	// Given
	fixture := newRoutingFixture(t, 3)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)

	// When
	err := New(fixture.repo, fixture.box, nil).Dispatch(
		context.Background(), parent.ID,
	)

	// Then
	if err != nil {
		t.Fatalf("dispatch fan-out: %v", err)
	}
	children, err := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)
	if err != nil || len(children) != 3 {
		t.Fatalf("children = %#v, %v; want three", children, err)
	}
	for index, child := range children {
		wantAgent := []string{"agent-a", "agent-b", "agent-c"}[index]
		if child.ParentDeploymentID.Int64 != parent.ID ||
			child.TargetAgentID.String != wantAgent || child.Status != "pending" {
			t.Fatalf("child %d = %#v", index, child)
		}
		dispatch, dispatchErr := fixture.repo.Queries.GetDeploymentDispatch(
			context.Background(), child.ID,
		)
		if dispatchErr != nil || dispatch.AssignedAgentID.String != wantAgent {
			t.Fatalf("child dispatch = %#v, %v", dispatch, dispatchErr)
		}
		payloadRow, payloadErr := fixture.repo.Queries.GetDeploymentPayload(
			context.Background(), child.ID,
		)
		if payloadErr != nil || payloadRow.DeploymentID != child.ID {
			t.Fatalf("child payload = %#v, %v", payloadRow, payloadErr)
		}
		plaintext, decryptErr := fixture.box.Decrypt(payloadRow.Ciphertext)
		var decoded payload
		if decryptErr != nil ||
			json.Unmarshal([]byte(plaintext), &decoded) != nil ||
			decoded.DeploymentID != child.ID {
			t.Fatalf("child payload identity = %#v, %v", decoded, decryptErr)
		}
	}
	if _, err := fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(), parent.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("parent dispatch error = %v, want no rows", err)
	}
}

func TestFanout_AllWithOneAgentStillCreatesOrdinaryChild(t *testing.T) {
	fixture := newRoutingFixture(t, 1)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)

	err := New(fixture.repo, fixture.box, nil).Dispatch(
		context.Background(), parent.ID,
	)

	if err != nil {
		t.Fatalf("dispatch one-agent fan-out: %v", err)
	}
	children, err := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)
	if err != nil || len(children) != 1 {
		t.Fatalf("children = %#v, %v; want one", children, err)
	}
	if children[0].TargetAgentID.String != "agent-a" {
		t.Fatalf("child = %#v; want agent-a", children[0])
	}
	if _, err := fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(), parent.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("parent dispatch error = %v, want no rows", err)
	}
	childDispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(), children[0].ID,
	)
	if err != nil || childDispatch.AssignedAgentID.String != "agent-a" {
		t.Fatalf("child dispatch = %#v, %v", childDispatch, err)
	}
	if _, err := fixture.repo.Queries.GetDeploymentPayload(
		context.Background(), children[0].ID,
	); err != nil {
		t.Fatalf("child payload: %v", err)
	}
}

func TestFanout_RollsBackExpansionWhenPayloadCannotBeBuilt(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)

	err := New(fixture.repo, nil, nil).Dispatch(context.Background(), parent.ID)

	if err == nil {
		t.Fatal("dispatch error = nil, want missing secret box")
	}
	children, listErr := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)
	if listErr != nil || len(children) != 0 {
		t.Fatalf("children = %#v, %v; want atomic rollback", children, listErr)
	}
}

func TestFanout_RestartDoesNotDuplicateChildren(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)
	dispatcher := New(fixture.repo, fixture.box, nil)
	if err := dispatcher.Dispatch(context.Background(), parent.ID); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}

	if err := dispatcher.Dispatch(context.Background(), parent.ID); err != nil {
		t.Fatalf("recovered dispatch: %v", err)
	}

	children, err := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)
	if err != nil || len(children) != 3 {
		t.Fatalf("children = %d, %v; want three", len(children), err)
	}
}

func TestParentAggregation_UsesTerminalPrecedenceAndTimestamps(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)
	if err := New(fixture.repo, fixture.box, nil).Dispatch(
		context.Background(), parent.ID,
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	children, _ := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)

	for index, child := range children {
		status := []string{"succeeded", "cancelled", "failed"}[index]
		if err := fixture.repo.Queries.UpdateDeploymentStatus(
			context.Background(), db.UpdateDeploymentStatusParams{
				ID: child.ID, Status: status,
				StartedAt: sql.NullInt64{
					Int64: int64(30 - index*10), Valid: true,
				},
				FinishedAt: sql.NullInt64{
					Int64: int64(40 + index*10), Valid: true,
				},
			},
		); err != nil {
			t.Fatalf("settle child: %v", err)
		}
		if err := deploymentstate.RecomputeParent(
			context.Background(), fixture.repo.Queries, child.ID,
		); err != nil {
			t.Fatalf("recompute parent: %v", err)
		}
		got, err := fixture.repo.Queries.GetDeployment(
			context.Background(), parent.ID,
		)
		if err != nil {
			t.Fatalf("get parent: %v", err)
		}
		if index < len(children)-1 &&
			(got.Status != "running" || got.FinishedAt.Valid) {
			t.Fatalf("unsettled parent = %#v, want running", got)
		}
	}

	got, err := fixture.repo.Queries.GetDeployment(
		context.Background(),
		parent.ID,
	)
	if err != nil || got.Status != "failed" || got.StartedAt.Int64 != 10 ||
		got.FinishedAt.Int64 != 60 {
		t.Fatalf("parent = %#v, %v; want failed [10,60]", got, err)
	}
}

func TestCancellationService_ParentFansOutIdempotently(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)
	if err := New(fixture.repo, fixture.box, nil).Dispatch(
		context.Background(), parent.ID,
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	service := NewCancellationService(fixture.repo, nil)

	state, err := service.Cancel(context.Background(), parent.ID)
	if err != nil || state != "cancelled" {
		t.Fatalf("cancel parent = %q, %v", state, err)
	}
	state, err = service.Cancel(context.Background(), parent.ID)
	if err != nil || state != "cancelled" {
		t.Fatalf("repeat cancel parent = %q, %v", state, err)
	}
	children, _ := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)
	for _, child := range children {
		if child.Status != "cancelled" {
			t.Fatalf("child = %#v, want cancelled", child)
		}
	}
}

func TestTargetUnavailable_FailsOnlyUnclaimedChild(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)
	if err := New(fixture.repo, fixture.box, nil).Dispatch(
		context.Background(), parent.ID,
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	children, _ := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)
	if _, err := fixture.repo.Queries.ClaimDeploymentDispatch(
		context.Background(),
		db.ClaimDeploymentDispatchParams{
			DeploymentID:    children[1].ID,
			AgentID:         sql.NullString{String: "agent-b", Valid: true},
			ClaimTokenHash:  make([]byte, 32),
			ClaimExpiresAt:  sql.NullInt64{Int64: 100, Valid: true},
			LastHeartbeatAt: sql.NullInt64{Int64: 1, Valid: true},
		},
	); err != nil {
		t.Fatalf("claim child: %v", err)
	}

	err := deploymentstate.FailUnclaimedAgentDeployments(
		context.Background(), fixture.repo.Queries,
		"agent-a", 50, "target agent disabled",
	)
	if err != nil {
		t.Fatalf("settle unavailable target: %v", err)
	}

	failed, _ := fixture.repo.Queries.GetDeployment(
		context.Background(), children[0].ID,
	)
	claimed, _ := fixture.repo.Queries.GetDeployment(
		context.Background(), children[1].ID,
	)
	parent, _ = fixture.repo.Queries.GetDeployment(
		context.Background(), parent.ID,
	)
	if failed.Status != "failed" || failed.FinishedAt.Int64 != 50 {
		t.Fatalf("unclaimed child = %#v, want failed", failed)
	}
	if claimed.Status != "pending" {
		t.Fatalf("claimed child = %#v, want pending", claimed)
	}
	if parent.Status != "pending" || parent.FinishedAt.Valid {
		t.Fatalf("parent = %#v, want unsettled pending", parent)
	}
}

func TestCancellationService_RejectsDirectChildCancellation(t *testing.T) {
	fixture := newRoutingFixture(t, 2)
	parent := fixture.createDeployment(t)
	freezeAll(t, fixture, parent.ID)
	if err := New(fixture.repo, fixture.box, nil).Dispatch(
		context.Background(), parent.ID,
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	children, _ := fixture.repo.Queries.ListDeploymentChildren(
		context.Background(), sql.NullInt64{Int64: parent.ID, Valid: true},
	)

	_, err := NewCancellationService(fixture.repo, nil).Cancel(
		context.Background(), children[0].ID,
	)

	if !errors.Is(err, ErrCancellationChild) {
		t.Fatalf("cancel child error = %v, want conflict", err)
	}
}

func freezeAll(t *testing.T, fixture routingFixture, deploymentID int64) {
	t.Helper()
	err := NewResolver(
		fixture.repo,
	).Freeze(context.Background(), deploymentID, Policy{
		Source: SourceRequest, Mode: TargetLabel,
		LabelID: fixture.label.ID, LabelName: fixture.label.Name,
		Strategy: StrategyAll,
	})
	if err != nil {
		t.Fatalf("freeze all routing: %v", err)
	}
}
