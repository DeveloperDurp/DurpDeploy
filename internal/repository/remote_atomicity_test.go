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

func runRemoteAtomicity(t *testing.T, repo *repository.Repository) {
	t.Helper()
	ctx := context.Background()
	first := claimArg("a")
	second := claimArg("a")
	second.DeploymentID = 2
	second.ClaimTokenHash[0] = 2
	args := []db.ClaimRemoteDeploymentParams{first, second}
	var wait sync.WaitGroup
	start := make(chan struct{})
	counts := make([]int64, 2)
	errs := make([]error, 2)
	for index := range args {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			counts[index], errs[index] = repo.ClaimRemoteDeployment(
				ctx,
				args[index],
			)
		}()
	}
	close(start)
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || counts[0]+counts[1] != 1 {
		t.Fatalf("claims rows=%v errors=%v", counts, errs)
	}
	winner := 0
	if counts[1] == 1 {
		winner = 1
	}
	old, next := args[winner], args[1-winner]

	expired := startArg(old)
	expired.Now = ni(110)
	n, err := repo.Queries.StartRemoteDeployment(ctx, expired)
	assertZero(t, n, err)
	n, err = repo.Queries.ExpireRemoteClaims(ctx, 110)
	assertOne(t, n, err)
	next.Now, next.ClaimExpiresAt = 110, 120
	n, err = repo.ClaimRemoteDeployment(ctx, next)
	assertOne(t, n, err)
	n, err = repo.Queries.StartRemoteDeployment(ctx, startArg(next))
	assertOne(t, n, err)

	logArg := repository.RemoteDeploymentLog{
		LockRemoteDeploymentClaimParams: db.LockRemoteDeploymentClaimParams{
			DeploymentID: next.DeploymentID,
			AgentID:      next.AgentID, ClaimTokenHash: next.ClaimTokenHash,
		},
		Sequence: 0, StepIndex: ni(0), Attempt: ni(1), Line: "one durable line",
	}
	logs := make([]db.DeploymentLog, 2)
	for index := range logs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			logs[index], errs[index] = repo.AppendRemoteDeploymentLog(
				ctx,
				logArg,
			)
		}()
	}
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || logs[0].ID == 0 ||
		logs[0].ID != logs[1].ID {
		t.Fatalf("dedup IDs=%d,%d errors=%v", logs[0].ID, logs[1].ID, errs)
	}

	for _, mutate := range []func(*db.FinishRemoteDeploymentParams){
		func(arg *db.FinishRemoteDeploymentParams) { arg.AgentID = "b" },
		func(arg *db.FinishRemoteDeploymentParams) { arg.ClaimTokenHash = old.ClaimTokenHash },
		func(arg *db.FinishRemoteDeploymentParams) { arg.DeploymentID = old.DeploymentID },
	} {
		arg := finishArg(next)
		mutate(&arg)
		n, err := repo.Queries.FinishRemoteDeployment(ctx, arg)
		assertZero(t, n, err)
	}
	n, err = repo.Queries.FinishRemoteDeployment(ctx, finishArg(next))
	assertOne(t, n, err)
	n, err = repo.Queries.FinishRemoteDeployment(ctx, finishArg(next))
	assertZero(t, n, err)
	_, err = repo.AppendRemoteDeploymentLog(ctx, logArg)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("terminal log callback: %v", err)
	}
}

func finishArg(
	arg db.ClaimRemoteDeploymentParams,
) db.FinishRemoteDeploymentParams {
	return db.FinishRemoteDeploymentParams{
		DeploymentID: arg.DeploymentID,
		AgentID:      arg.AgentID, ClaimTokenHash: arg.ClaimTokenHash,
		Now: ni(115), State: "succeeded",
	}
}
