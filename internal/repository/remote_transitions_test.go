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
	for _, mutate := range []func(*db.ClaimRemoteDeploymentStepParams){
		func(a *db.ClaimRemoteDeploymentStepParams) { a.DeploymentID = 0 },
		func(a *db.ClaimRemoteDeploymentStepParams) { a.StepIndex = -1 },
		func(a *db.ClaimRemoteDeploymentStepParams) { a.Attempt = 0 },
		func(a *db.ClaimRemoteDeploymentStepParams) { a.AgentID = ns("") },
		func(a *db.ClaimRemoteDeploymentStepParams) { a.AgentID = ns("' OR 1=1 --") },
		func(a *db.ClaimRemoteDeploymentStepParams) { a.ClaimExpiresAt = a.Now },
	} {
		a := claimArg("a")
		mutate(&a)
		n, err := r.ClaimRemoteStep(ctx, a)
		assertZero(t, n, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	n, err := r.ClaimRemoteStep(cancelled, claimArg("a"))
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled claim rows=%d error=%v", n, err)
	}
	n, err = r.ClaimRemoteStep(ctx, claimArg("a"))
	assertOne(t, n, err)
	n, err = r.Queries.FinishRemoteDeploymentStep(ctx, finishArg(claimArg("a")))
	assertZero(t, n, err)
	t.Log(
		"zero/negative/malformed IDs, expired claim, finish-before-start: rows=0; cancelled context preserved; fresh claim=1",
	)
}

func TestDeploymentStepCancellationAndHeartbeat(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	a := claimArg("a")
	n, err := r.ClaimRemoteStep(ctx, a)
	assertOne(t, n, err)
	n, err = r.Queries.StartRemoteDeploymentStep(ctx, startArg(a))
	assertOne(t, n, err)
	hb := db.HeartbeatRemoteDeploymentStepParams{
		DeploymentID: 1, StepIndex: 0, Attempt: 1,
		AgentID: a.AgentID, ClaimTokenHash: a.ClaimTokenHash, Now: ni(105),
	}
	n, err = r.Queries.HeartbeatRemoteDeploymentStep(ctx, hb)
	assertOne(t, n, err)
	hb.Now = ni(104)
	n, err = r.Queries.HeartbeatRemoteDeploymentStep(ctx, hb)
	assertZero(t, n, err)
	n, err = r.CancelStepDeployment(
		ctx,
		db.RequestDeploymentStepCancellationParams{
			DeploymentID: 1,
			Now:          ni(106),
		},
	)
	assertOne(t, n, err)
	n, err = r.Queries.FinishRemoteDeploymentStep(ctx, finishArg(a))
	assertZero(t, n, err)
	n, err = r.Queries.ExpireRemoteCancellation(ctx,
		db.ExpireRemoteCancellationParams{Now: ni(120), StaleBefore: ni(110)})
	assertOne(t, n, err)
	a.DeploymentID = 2
	n, err = r.ClaimRemoteStep(ctx, a)
	assertZero(t, n, err)
	row, err := r.Queries.GetDeploymentStepAttempt(
		ctx,
		db.GetDeploymentStepAttemptParams{
			DeploymentID: 1,
			StepIndex:    0,
			Attempt:      1,
		},
	)
	if err != nil || row.State != "cancel_uncertain" ||
		row.LastHeartbeatAt.Int64 != 105 {
		t.Fatalf(
			"state=%s heartbeat=%d error=%v",
			row.State,
			row.LastHeartbeatAt.Int64,
			err,
		)
	}
	t.Log(
		"heartbeat=105; backwards heartbeat=0; cancellation=1; late finish=0; cancel_uncertain retains capacity",
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
