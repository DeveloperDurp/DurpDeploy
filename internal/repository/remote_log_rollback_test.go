package repository_test

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
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
	arg := repository.RemoteDeploymentLog{
		LockRemoteDeploymentClaimParams: db.LockRemoteDeploymentClaimParams{
			DeploymentID: 1,
			AgentID:      a.AgentID, ClaimTokenHash: a.ClaimTokenHash,
		}, Sequence: -1, StepIndex: ni(0), Attempt: ni(1), Line: "fixture line",
	}
	_, err = r.AppendRemoteDeploymentLog(ctx, arg)
	if err == nil {
		t.Fatal("negative sequence accepted")
	}
	var count int
	err = r.DB.QueryRow("SELECT COUNT(*) FROM deployment_logs").Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("rollback logs=%d error=%v", count, err)
	}
	arg.Sequence = 0
	first, err := r.AppendRemoteDeploymentLog(ctx, arg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.AppendRemoteDeploymentLog(ctx, arg)
	if err != nil || first.ID == 0 || second.ID != first.ID {
		t.Fatalf("dedup IDs=%d,%d error=%v", first.ID, second.ID, err)
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
			Now:          ni(105),
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
