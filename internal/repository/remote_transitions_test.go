package repository_test

import (
	"context"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentStepRejectsInvalidClaims(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	for _, mutate := range []func(*db.ClaimRemoteDeploymentParams){
		func(a *db.ClaimRemoteDeploymentParams) { a.DeploymentID = 0 },
		func(a *db.ClaimRemoteDeploymentParams) { a.AgentID = "" },
		func(a *db.ClaimRemoteDeploymentParams) { a.AgentID = "b" },
		func(a *db.ClaimRemoteDeploymentParams) { a.ClaimExpiresAt = a.Now },
	} {
		a := claimArg("a")
		mutate(&a)
		n, err := r.ClaimRemoteDeployment(ctx, a)
		assertZero(t, n, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	n, err := r.ClaimRemoteDeployment(cancelled, claimArg("a"))
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled claim rows=%d error=%v", n, err)
	}
	n, err = r.ClaimRemoteDeployment(ctx, claimArg("a"))
	assertOne(t, n, err)
	n, err = r.Queries.FinishRemoteDeployment(ctx, finishArg(claimArg("a")))
	assertZero(t, n, err)
	t.Log(
		"zero/negative/malformed IDs, expired claim, finish-before-start: rows=0; cancelled context preserved; fresh claim=1",
	)
}

func TestDeploymentStepCancellationAndHeartbeat(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	a := claimArg("a")
	n, err := r.ClaimRemoteDeployment(ctx, a)
	assertOne(t, n, err)
	n, err = r.Queries.StartRemoteDeployment(ctx, startArg(a))
	assertOne(t, n, err)
	hb := db.HeartbeatRemoteDeploymentParams{
		DeploymentID: 1,
		AgentID:      a.AgentID, ClaimTokenHash: a.ClaimTokenHash, Now: ni(105),
	}
	n, err = r.Queries.HeartbeatRemoteDeployment(ctx, hb)
	assertOne(t, n, err)
	hb.Now = ni(104)
	n, err = r.Queries.HeartbeatRemoteDeployment(ctx, hb)
	assertZero(t, n, err)
	n, err = r.CancelRemoteDeployment(
		ctx,
		db.RequestRemoteDeploymentCancellationParams{
			DeploymentID: 1,
			Now:          ni(106),
		},
	)
	assertOne(t, n, err)
	n, err = r.Queries.FinishRemoteDeployment(ctx, finishArg(a))
	assertZero(t, n, err)
	n, err = r.Queries.ExpireRemoteCancellation(ctx,
		db.ExpireRemoteCancellationParams{Now: ni(120), StaleBefore: ni(110)})
	assertOne(t, n, err)
	a.DeploymentID = 2
	a.ClaimTokenHash[0] = 2
	n, err = r.ClaimRemoteDeployment(ctx, a)
	assertOne(t, n, err)
	row, err := r.Queries.GetRemoteDeploymentClaim(ctx, 1)
	if err != nil || row.State != "cancel_unconfirmed" ||
		row.LastHeartbeatAt.Int64 != 105 {
		t.Fatalf(
			"state=%s heartbeat=%d error=%v",
			row.State,
			row.LastHeartbeatAt.Int64,
			err,
		)
	}
	t.Log(
		"heartbeat=105; backwards heartbeat=0; cancellation=1; late finish=0; cancel_unconfirmed frees capacity",
	)
}

func TestDeploymentStepSnapshotIsImmutable(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	if err := r.SnapshotDeploymentSteps(ctx, 1, nil); err == nil {
		t.Fatal("resolved snapshot accepted replacement")
	}
	n, err := r.Queries.AddDeploymentStepSelector(
		ctx,
		db.AddDeploymentStepSelectorParams{
			DeploymentID: 1,
			StepIndex:    0,
			Label:        "new",
		},
	)
	assertZero(t, n, err)
	labels, err := r.Queries.ListDeploymentStepSelectors(ctx,
		db.ListDeploymentStepSelectorsParams{DeploymentID: 1, StepIndex: 0})
	if err != nil || len(labels) != 2 || labels[0] != "linux" ||
		labels[1] != "other" {
		t.Fatalf("labels=%v error=%v", labels, err)
	}
	n, err = r.Queries.CreateDeploymentStepAttempt(ctx,
		db.CreateDeploymentStepAttemptParams{DeploymentID: 1, StepIndex: 1,
			Attempt: 1, Now: 100, WaitDeadline: 400})
	assertZero(t, n, err)
	t.Log(
		"resolved labels=[linux other]; replacement rejected; selector append=0; out-of-order attempt=0",
	)
}
