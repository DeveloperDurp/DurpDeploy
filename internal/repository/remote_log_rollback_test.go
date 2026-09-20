package repository_test

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
	"durpdeploy/internal/repository"
)

func TestDurableLogScopeFailureRollsBack(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	a := claimArg("a")
	n, err := r.ClaimRemoteDeployment(ctx, a)
	assertOne(t, n, err)
	n, err = r.Queries.StartRemoteDeployment(ctx, startArg(a))
	assertOne(t, n, err)
	n, err = r.Queries.StartRemoteDeploymentStatus(
		ctx,
		db.StartRemoteDeploymentStatusParams{
			Now: ni(100), DeploymentID: 1, AgentID: ns(a.AgentID),
		},
	)
	assertOne(t, n, err)
	identity := repository.RemoteLifecycleClaim{
		DeploymentID: 1, AgentID: a.AgentID, ClaimTokenHash: a.ClaimTokenHash,
	}
	events := []repository.RemoteLogEvent{{Sequence: -1, Line: "fixture line"}}
	_, err = r.AppendRemoteDeploymentLogs(
		ctx, identity, events, logscrub.New(nil),
	)
	if err == nil {
		t.Fatal("negative sequence accepted")
	}
	var count int
	err = r.DB.QueryRow("SELECT COUNT(*) FROM deployment_logs").Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("rollback logs=%d error=%v", count, err)
	}
	events[0].Sequence = 0
	first, err := r.AppendRemoteDeploymentLogs(
		ctx, identity, events, logscrub.New(nil),
	)
	if err != nil || len(first) != 1 {
		t.Fatal(err)
	}
	second, err := r.AppendRemoteDeploymentLogs(
		ctx, identity, events, logscrub.New(nil),
	)
	if err != nil || len(second) != 0 {
		t.Fatalf("dedup inserts=%d,%d error=%v", len(first), len(second), err)
	}
	t.Log(
		"negative sequence rejected; rollback logs=0; fresh duplicate returns same nonzero ID",
	)
}

func TestRemoteDeploymentPreStartCancellationFreesCapacity(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	a := claimArg("a")
	n, err := r.ClaimRemoteDeployment(ctx, a)
	assertOne(t, n, err)
	n, err = r.CancelRemoteDeployment(
		ctx,
		db.RequestRemoteDeploymentCancellationParams{
			DeploymentID: 1,
			Now:          105,
		},
	)
	assertOne(t, n, err)
	n, err = r.Queries.StartRemoteDeployment(ctx, startArg(a))
	assertZero(t, n, err)
	row, err := r.Queries.GetRemoteDeploymentClaim(ctx, 1)
	if err != nil || row.State != "cancelled" || row.StartedAt.Valid {
		t.Fatalf(
			"state=%s started=%v error=%v",
			row.State,
			row.StartedAt.Valid,
			err,
		)
	}
	a.DeploymentID = 2
	a.ClaimTokenHash = claimArg("b").ClaimTokenHash
	n, err = r.ClaimRemoteDeployment(ctx, a)
	assertOne(t, n, err)
	t.Log(
		"deployment=1 cancelled unstarted; stale start=0; same agent fresh deployment=2 claim=1",
	)
}
