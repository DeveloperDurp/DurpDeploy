package repository

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestPollRetriesRolledBackSQLServerClaims(t *testing.T) {
	for _, kind := range []string{"deployment", "step"} {
		t.Run(kind, func(t *testing.T) {
			repo, locker := openDeploymentCreationEngine(t,
				newDeploymentCreationEngine(t, "SQLServer"))
			deployment := createLegacyRemoteDeployment(t, repo)
			table := "remote_deployment_claims"
			claim := repo.ClaimRemoteDeploymentPayload
			if kind == "step" {
				table, claim = "remote_step_runs", repo.ClaimRemoteStepPayload
				seedDeadlockStep(t, repo, deployment)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			// The trigger makes the real claim transaction lock row 1 then row 2.
			if _, err := repo.DB.ExecContext(
				ctx,
				`CREATE TABLE poll_retry_locks (
id INT PRIMARY KEY, attempts INT NOT NULL);
INSERT INTO poll_retry_locks VALUES (1,0),(2,0);`,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.DB.ExecContext(
				ctx,
				`CREATE TRIGGER poll_retry_deadlock
ON `+table+` AFTER UPDATE AS
BEGIN
  IF EXISTS (SELECT 1 FROM inserted WHERE state='claimed')
  BEGIN
    UPDATE poll_retry_locks SET attempts=attempts+1 WHERE id=1;
    UPDATE poll_retry_locks SET attempts=attempts+1 WHERE id=2;
  END
END;`,
			); err != nil {
				t.Fatal(err)
			}
			conn, err := locker.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// Force the application transaction to be the deadlock victim.
			if _, err := tx.ExecContext(ctx, `SET DEADLOCK_PRIORITY HIGH;
UPDATE poll_retry_locks SET attempts=attempts WHERE id=2;`); err != nil {
				t.Fatal(err)
			}
			var spid int
			if err := tx.QueryRowContext(ctx, "SELECT @@SPID").
				Scan(&spid); err != nil {
				t.Fatal(err)
			}
			var preparations atomic.Int32
			done := make(chan error, 1)
			go func() {
				_, claimed, err := claim(ctx, "race-agent",
					func(RemotePayloadSnapshot) (RemotePreparedClaim, error) {
						preparations.Add(1)
						return RemotePreparedClaim{TokenHash: make([]byte, 32),
							Ciphertext: []byte("sealed")}, nil
					})
				if err == nil && !claimed {
					err = sql.ErrNoRows
				}
				done <- err
			}()
			// Observe this claim blocked on our transaction's row 2 before
			// requesting its row 1; this forms a real, repeatable lock cycle.
			for {
				var waiting int
				if err := repo.DB.QueryRowContext(ctx, `SELECT COUNT(*)
FROM sys.dm_exec_requests WHERE blocking_session_id=?
AND database_id=DB_ID() AND wait_type LIKE 'LCK_M%'`, spid).
					Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting > 0 {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("claim did not reach lock cycle: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if _, err := tx.ExecContext(
				ctx,
				"UPDATE poll_retry_locks SET attempts=attempts WHERE id=1",
			); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf(
					"rolled-back %s claim was not recovered: %v",
					kind,
					err,
				)
			}
			if preparations.Load() != 2 {
				t.Fatalf("payload preparations=%d, want 2", preparations.Load())
			}
			var attempts int
			if err := repo.DB.QueryRowContext(
				ctx,
				"SELECT SUM(attempts) FROM poll_retry_locks",
			).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if attempts != 2 {
				t.Fatalf(
					"partial victim writes survived rollback: attempts=%d",
					attempts,
				)
			}
		})
	}
}

func seedDeadlockStep(t *testing.T, repo *Repository, d db.Deployment) {
	t.Helper()
	if err := repo.SnapshotDeploymentSteps(
		t.Context(),
		d.ID,
		[]DeploymentStepSnapshot{
			{CreateDeploymentStepParams: db.CreateDeploymentStepParams{
				Name: "remote", ScriptBody: "echo remote", Interpreter: "bash",
				ExecutionTarget: "agent",
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(
		t.Context(),
		`DELETE FROM remote_deployment_claims
WHERE deployment_id=?;
UPDATE deployments SET status='running',assigned_agent_id=NULL WHERE id=?;
INSERT INTO agent_interpreters(agent_id,interpreter) VALUES('race-agent','bash');
INSERT INTO agent_environment_labels(agent_id,environment_id)
VALUES('race-agent',?);`,
		d.ID,
		d.ID,
		d.EnvironmentID,
	); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.QueueRemoteStepRuns(
		t.Context(),
		d.ID,
		0,
	); err != nil ||
		count != 1 {
		t.Fatalf("queue step count=%d: %v", count, err)
	}
}
