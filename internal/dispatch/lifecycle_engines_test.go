package dispatch

import (
	"bytes"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStartMaintenanceExactDeadlineAcrossDatabases(t *testing.T) {
	for _, name := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(name, func(t *testing.T) {
			engine := provisionLifecycleEngine(t, name)
			first, second := openLifecycleEngine(t, engine)
			seedExactDeadlineClaim(t, first)
			identity := repository.RemoteLifecycleClaim{
				DeploymentID:   1,
				AgentID:        "race-agent",
				ClaimTokenHash: bytes.Repeat([]byte{1}, 32),
			}
			start := make(chan struct{})
			var wait sync.WaitGroup
			wait.Add(2)
			var startErr, maintainErr error
			go func() {
				defer wait.Done()
				<-start
				startErr = second.StartRemoteDeployment(t.Context(), identity)
			}()
			go func() {
				defer wait.Done()
				<-start
				maintainErr = New(first).Maintain(t.Context())
			}()
			close(start)
			wait.Wait()
			if maintainErr != nil || !errors.Is(
				startErr,
				repository.ErrRemoteLifecycleConflict,
			) {
				t.Fatalf("start error=%v maintenance error=%v",
					startErr, maintainErr)
			}
			claim, err := first.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
			if err != nil || claim.State != "waiting" || claim.StartedAt.Valid {
				t.Fatalf("deadline claim=%+v error=%v", claim, err)
			}
		})
	}
}

func TestRepeatedUnstartedClaimsFailAcrossDatabases(t *testing.T) {
	for _, name := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(name, func(t *testing.T) {
			first, second := openLifecycleEngine(
				t, provisionLifecycleEngine(t, name),
			)
			seedExactDeadlineClaim(t, first)
			checkLegacyPreStartFailure(t, first, second)
			checkStepPreStartFailure(t, first, second, name == "PostgreSQL")
		})
	}
}

func checkLegacyPreStartFailure(
	t *testing.T,
	first, second *repository.Repository,
) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, New(first).Maintain(ctx))
	claim, err := second.Queries.GetRemoteDeploymentClaim(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "waiting", claim.State)
	require.EqualValues(t, 1, claim.ExpiredClaims)
	_, err = first.DB.ExecContext(ctx, `UPDATE remote_deployment_claims
SET state='claimed', claim_expires_at=1, claim_token_hash=?,
    ciphertext='retry', last_heartbeat_at=1
WHERE deployment_id=1`, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	require.NoError(t, New(first).Maintain(ctx))
	claim, err = second.Queries.GetRemoteDeploymentClaim(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "cancelled", claim.State)
	require.Equal(t, "remote_agent_did_not_start", claim.Reason.String)
	require.Empty(t, claim.ClaimTokenHash)
	require.False(t, claim.Ciphertext.Valid)
	require.True(t, claim.FinishedAt.Valid)
	deployment, err := second.Queries.GetDeployment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "failed", deployment.Status)
}

func checkStepPreStartFailure(
	t *testing.T,
	first, second *repository.Repository,
	checkLogOrder bool,
) {
	t.Helper()
	ctx := t.Context()
	deployment, err := first.Queries.CreateDeployment(
		ctx, db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "running",
		},
	)
	require.NoError(t, err)
	id := deployment.ID
	_, err = first.DB.ExecContext(ctx, `INSERT INTO deployment_steps
(deployment_id,step_index,name,script_body,execution_target)
VALUES(?,0,'producer','echo stage','agent'),(?,1,'consumer','echo remote','agent')`,
		id, id)
	require.NoError(t, err)
	_, err = first.DB.ExecContext(ctx, `INSERT INTO remote_step_runs
(deployment_id,step_index,agent_id,state,started_at,finished_at)
VALUES(?,0,'race-agent','succeeded',1,1)`, id)
	require.NoError(t, err)
	_, err = first.DB.ExecContext(ctx, `INSERT INTO remote_step_runs
(deployment_id,step_index,agent_id,state,claim_expires_at,expired_claims,
 claim_token_hash,ciphertext,last_heartbeat_at)
VALUES(?,1,'race-agent','claimed',1,1,?,'retry',1)`,
		id, bytes.Repeat([]byte{3}, 32))
	require.NoError(t, err)
	if checkLogOrder {
		checkExpiryWaitsForLogWriter(t, first, second, id)
	} else {
		require.NoError(t, New(first).Maintain(ctx))
	}
	require.NoError(t, New(first).Maintain(ctx))
	runs, err := second.Queries.ListRemoteStepRuns(
		ctx, db.ListRemoteStepRunsParams{DeploymentID: id, StepIndex: 1},
	)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "failed", runs[0].State)
	require.EqualValues(t, 2, runs[0].ExpiredClaims)
	require.Empty(t, runs[0].ClaimTokenHash)
	require.Equal(t, sql.NullString{}, runs[0].Ciphertext)
	deployment, err = second.Queries.GetDeployment(ctx, id)
	require.NoError(t, err)
	require.Equal(
		t,
		"running",
		deployment.Status,
		"runner owns staging cleanup",
	)
	var count int
	require.NoError(t, second.DB.QueryRowContext(ctx, `SELECT COUNT(*)
FROM deployment_logs WHERE deployment_id=?
AND line LIKE '%did not start the remote step%'`, id).Scan(&count))
	require.Equal(t, 1, count)
}

func checkExpiryWaitsForLogWriter(
	t *testing.T, first, second *repository.Repository, id int64,
) {
	t.Helper()
	ctx := t.Context()
	_, err := first.DB.ExecContext(ctx, `INSERT INTO deployment_step_sources
(deployment_id, steps_json) VALUES(?, '[]')`, id)
	require.NoError(t, err)
	tx, err := first.DB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer testutil.Rollback(t, tx)
	q := first.Queries.WithTx(tx)
	locked, err := q.LockDeploymentLogStream(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 1, locked)
	earlier, err := q.CreateStepDeploymentLog(
		ctx, db.CreateStepDeploymentLogParams{
			DeploymentID: id, Line: "earlier writer",
		},
	)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- New(second).Maintain(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("expiry bypassed the active log writer: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("expiry did not finish after the log writer committed")
	}
	logs, err := first.Queries.ListDeploymentLogsAfter(
		ctx, db.ListDeploymentLogsAfterParams{
			DeploymentID: id, ID: earlier.ID, BatchLimit: 10,
		},
	)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Contains(t, logs[0].Line, "did not start the remote step")
}
