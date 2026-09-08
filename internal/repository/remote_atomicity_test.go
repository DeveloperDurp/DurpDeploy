package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRemoteAgentClaimAndLogAtomicity(t *testing.T) {
	runRemoteAtomicity(t, remoteFixture(t))
}

func runRemoteAtomicity(t *testing.T, r *repository.Repository) {
	t.Helper()
	ctx := context.Background()
	args := []db.ClaimRemoteDeploymentStepParams{claimArg("a"), claimArg("b")}
	var wg sync.WaitGroup
	start := make(chan struct{})
	counts := make([]int64, 2)
	errs := make([]error, 2)
	for i := range args {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			counts[i], errs[i] = r.ClaimRemoteStep(ctx, args[i])
		}()
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || counts[0]+counts[1] != 1 {
		t.Fatalf("claims rows=%v errors=%v", counts, errs)
	}
	winner := 0
	if counts[1] == 1 {
		winner = 1
	}
	old, next := args[winner], args[1-winner]
	t.Logf("concurrent eligible claims: rows=%v; exactly one winner", counts)
	busy := old
	busy.DeploymentID = 2
	n, err := r.ClaimRemoteStep(ctx, busy)
	assertZero(t, n, err)
	expired := startArg(old)
	expired.Now = ni(110)
	n, err = r.Queries.StartRemoteDeploymentStep(ctx, expired)
	assertZero(t, n, err)
	n, err = r.Queries.ExpireRemoteClaims(ctx, 110)
	assertOne(t, n, err)
	n, err = r.Queries.ExpireRemoteClaims(ctx, 110)
	assertZero(t, n, err)
	next.Now, next.ClaimExpiresAt = ni(110), ni(120)
	n, err = r.ClaimRemoteStep(ctx, next)
	assertOne(t, n, err)
	n, err = r.Queries.StartRemoteDeploymentStep(ctx, startArg(old))
	assertZero(t, n, err)
	n, err = r.Queries.StartRemoteDeploymentStep(ctx, startArg(next))
	assertOne(t, n, err)
	n, err = r.Queries.ExpireRemoteClaims(ctx, 130)
	assertZero(t, n, err)
	n, err = r.ClaimRemoteStep(ctx, old)
	assertZero(t, n, err)
	t.Log(
		"pre-start expiry=1; expired start=0; old callback=0; started expiry/reclaim=0",
	)
	logArg := repository.RemoteStepLog{
		LockRemoteLogAttemptParams: db.LockRemoteLogAttemptParams{
			DeploymentID: 1, StepIndex: 0, Attempt: 1,
			AgentID: next.AgentID, ClaimTokenHash: next.ClaimTokenHash,
		}, Sequence: 0, Line: "one durable line",
	}
	logs := make([]db.DeploymentLog, 2)
	for i := range logs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logs[i], errs[i] = r.AppendRemoteStepLog(ctx, logArg)
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || logs[0].ID == 0 ||
		logs[0].ID != logs[1].ID {
		t.Fatalf("dedup IDs=%d,%d errors=%v", logs[0].ID, logs[1].ID, errs)
	}
	var logCount, scopeCount int
	if err := r.DB.QueryRow("SELECT COUNT(*) FROM deployment_logs").Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if err := r.DB.QueryRow("SELECT COUNT(*) FROM deployment_log_scopes").Scan(&scopeCount); err != nil {
		t.Fatal(err)
	}
	if logCount != 1 || scopeCount != 1 {
		t.Fatalf("logs=%d scopes=%d", logCount, scopeCount)
	}
	t.Logf(
		"final log rows: logs=%d scopes=%d deployment=1 step=0 attempt=1 sequence=0 log_id=%d",
		logCount,
		scopeCount,
		logs[0].ID,
	)
	for _, mutate := range []func(*db.FinishRemoteDeploymentStepParams){
		func(a *db.FinishRemoteDeploymentStepParams) { a.AgentID = old.AgentID },
		func(a *db.FinishRemoteDeploymentStepParams) { a.ClaimTokenHash = old.ClaimTokenHash },
		func(a *db.FinishRemoteDeploymentStepParams) { a.DeploymentID = 2 },
		func(a *db.FinishRemoteDeploymentStepParams) { a.StepIndex = 1 },
		func(a *db.FinishRemoteDeploymentStepParams) { a.Attempt = 2 },
	} {
		arg := finishArg(next)
		mutate(&arg)
		n, err := r.Queries.FinishRemoteDeploymentStep(ctx, arg)
		assertZero(t, n, err)
	}
	cursor, err := r.Queries.GetDeploymentStepCursor(ctx, 1)
	if err != nil || cursor.StepIndex != 0 {
		t.Fatalf("stale cursor=%v err=%v", cursor, err)
	}
	n, err = r.Queries.FinishRemoteDeploymentStep(ctx, finishArg(next))
	assertOne(t, n, err)
	n, err = r.Queries.FinishRemoteDeploymentStep(ctx, finishArg(next))
	assertZero(t, n, err)
	cursor, err = r.Queries.GetDeploymentStepCursor(ctx, 1)
	if err != nil || cursor.StepIndex != 1 {
		t.Fatalf("cursor=%v err=%v", cursor, err)
	}
	_, err = r.AppendRemoteStepLog(ctx, logArg)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("terminal callback: %v", err)
	}
	t.Log(
		"final attempt: deployment=1 step=0 attempt=1 state=succeeded; cursor=1; stale identities and duplicate result=0",
	)
}

func finishArg(
	arg db.ClaimRemoteDeploymentStepParams,
) db.FinishRemoteDeploymentStepParams {
	return db.FinishRemoteDeploymentStepParams{
		DeploymentID: arg.DeploymentID, StepIndex: arg.StepIndex,
		Attempt: arg.Attempt, AgentID: arg.AgentID, ClaimTokenHash: arg.ClaimTokenHash,
		Now: ni(115), State: "succeeded",
	}
}
