package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func queueRecoveryFixture(t *testing.T, repo *Repository) (int64, int64) {
	t.Helper()
	owner, err := repo.CreateDeployment(t.Context(), db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	require.NoError(t, err)
	next, err := repo.CreateDeployment(t.Context(), db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	require.NoError(t, err)
	return owner.Deployment.ID, next.Deployment.ID
}

func TestQueueRecoveryAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, second := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		owner, next := queueRecoveryFixture(t, repo)
		project, err := repo.Queries.CreateProject(
			t.Context(),
			db.CreateProjectParams{Name: "other recovery pair"},
		)
		require.NoError(t, err)
		release, err := repo.Queries.CreateRelease(
			t.Context(),
			db.CreateReleaseParams{
				ProjectID: project.ID,
				Version:   "other",
				StepsJson: "[]",
			},
		)
		require.NoError(t, err)
		other, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID:     release.ID,
				EnvironmentID: 1,
				Status:        "pending",
			},
		)
		require.NoError(t, err)
		slots, err := repo.DeploymentQueueDiagnostics(t.Context())
		require.NoError(t, err)
		require.Len(t, slots, 2)
		changed, err := repo.ReconcileDeploymentQueue(
			t.Context(),
			owner,
			db.CreateAuditLogParams{},
		)
		require.ErrorIs(t, err, ErrQueueRecoveryBlocked)
		require.False(t, changed)
		// Persisted confirmation permits reconciliation, without changing the outcome.
		_, err = repo.DB.ExecContext(
			t.Context(),
			"UPDATE deployments SET cleanup_confirmed_at=1 WHERE id=?",
			owner,
		)
		require.NoError(t, err)
		require.NoError(
			t,
			repo.Queries.UpdateDeploymentStatus(
				t.Context(),
				db.UpdateDeploymentStatusParams{
					ID: owner, Status: "failed",
				},
			),
		)
		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make(chan bool, 2)
		failures := make(chan error, 2)
		for _, conn := range []*Repository{repo, second} {
			wg.Go(func() {
				<-start
				changed, err := conn.ReconcileDeploymentQueue(
					t.Context(),
					owner,
					db.CreateAuditLogParams{},
				)
				results <- changed
				failures <- err
			})
		}
		close(start)
		wg.Wait()
		close(results)
		close(failures)
		count := 0
		for changed := range results {
			if changed {
				count++
			}
		}
		for err := range failures {
			require.NoError(t, err)
		}
		require.Equal(t, 1, count)
		d, err := repo.Queries.GetDeployment(t.Context(), next)
		require.NoError(t, err)
		require.Equal(t, "pending", d.Status)
		finished, err := repo.Queries.GetDeployment(t.Context(), owner)
		require.NoError(t, err)
		require.Equal(t, "failed", finished.Status)
		require.True(t, finished.CleanupConfirmedAt.Valid)
		otherOwner, err := repo.Queries.GetDeploymentSlot(
			t.Context(),
			other.Deployment.ID,
		)
		require.NoError(t, err)
		require.Equal(t, other.Deployment.ID, otherOwner)
		var events int
		require.NoError(t, repo.DB.QueryRowContext(
			t.Context(),
			"SELECT COUNT(*) FROM audit_log WHERE action='reconcile_deployment_queue'",
		).Scan(&events))
		require.Equal(t, 1, events)
	})
}

func TestQueueRecoveryBufferPredicateAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		owner, _ := queueRecoveryFixture(t, repo)
		_, err := repo.DB.ExecContext(
			t.Context(),
			`INSERT INTO deployment_steps(deployment_id,step_index,name,script_body) VALUES(?,0,'buffer','true')`,
			owner,
		)
		require.NoError(t, err)
		_, err = repo.DB.ExecContext(
			t.Context(),
			`INSERT INTO remote_step_runs(deployment_id,step_index,agent_id,state) VALUES(?,0,'race-agent','waiting')`,
			owner,
		)
		require.NoError(t, err)
		_, err = repo.DB.ExecContext(
			t.Context(),
			`INSERT INTO remote_deployment_claims(deployment_id,agent_id,state) VALUES(?,'race-agent','waiting')`,
			owner,
		)
		require.NoError(t, err)
		for _, scenario := range []struct {
			buffer any
			count  int64
		}{
			{nil, 0}, {"", 0}, {"encrypted-receipt", 2},
		} {
			for _, table := range []string{"remote_step_runs", "remote_deployment_claims"} {
				_, err := repo.DB.ExecContext(
					t.Context(),
					"UPDATE "+table+" SET log_buffer_ciphertext=? WHERE deployment_id=?",
					scenario.buffer,
					owner,
				)
				require.NoError(t, err)
			}
			count, err := repo.Queries.CountDeploymentBufferedRemoteLogs(
				t.Context(),
				owner,
			)
			require.NoError(t, err)
			require.Equal(t, scenario.count, count)
		}
	})
}

func TestQueueRecoveryPreservesCleanupAndLogs(t *testing.T) {
	for _, table := range []string{"remote_step_runs", "remote_deployment_claims"} {
		for _, state := range []string{"started", "lost", "cancel_unconfirmed", "cleanup_unconfirmed", "failed"} {
			t.Run(table+"/"+state, func(t *testing.T) {
				testQueueRecoveryRemoteState(t, table, state)
			})
		}
	}
}

func testQueueRecoveryRemoteState(t *testing.T, table, state string) {
	t.Helper()
	repo, _ := openDeploymentCreationEngine(
		t,
		newDeploymentCreationEngine(t, "SQLite"),
	)
	owner, next := queueRecoveryFixture(t, repo)
	require.NoError(
		t,
		repo.Queries.UpdateDeploymentStatus(
			t.Context(),
			db.UpdateDeploymentStatusParams{
				ID:     owner,
				Status: "failed",
			},
		),
	)
	statement := `INSERT INTO remote_deployment_claims(deployment_id,agent_id,state,log_buffer_ciphertext,
claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,started_at,finished_at,cancel_requested_at)
VALUES(?,'race-agent',?,?,zeroblob(32),'fixture',100,1,1,?,?)`
	if table == "remote_step_runs" {
		_, err := repo.DB.ExecContext(
			t.Context(),
			`INSERT INTO deployment_steps(deployment_id,step_index,name,script_body) VALUES(?,0,'recovery','true')`,
			owner,
		)
		require.NoError(t, err)
		statement = `INSERT INTO remote_step_runs(deployment_id,step_index,agent_id,state,log_buffer_ciphertext,
claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,started_at,finished_at,cancel_requested_at)
VALUES(?,0,'race-agent',?,?,zeroblob(32),'fixture',100,1,1,?,?)`
	}
	var buffer any
	if state == "failed" {
		buffer = "retained"
	}
	var finished, cancelled any
	if state != "started" {
		finished = 2
	}
	if state == "cancel_unconfirmed" {
		cancelled = 1
	}
	_, err := repo.DB.ExecContext(
		t.Context(),
		statement,
		owner,
		state,
		buffer,
		finished,
		cancelled,
	)
	require.NoError(t, err)
	changed, err := repo.ReconcileDeploymentQueue(
		t.Context(),
		owner,
		db.CreateAuditLogParams{},
	)
	require.ErrorIs(t, err, ErrQueueRecoveryBlocked)
	require.False(t, changed)
	slots, err := repo.DeploymentQueueDiagnostics(t.Context())
	require.NoError(t, err)
	require.Len(t, slots, 1)
	require.Len(t, slots[0].RemoteRuns, 1)
	if state == "cleanup_unconfirmed" {
		_, err = repo.DB.ExecContext(
			t.Context(),
			"UPDATE "+table+" SET cleanup_confirmed_at=1 WHERE deployment_id=?",
			owner,
		)
	} else {
		_, err = repo.DB.ExecContext(
			t.Context(),
			"UPDATE "+table+" SET state='cleanup_unconfirmed',cleanup_confirmed_at=2,finished_at=2,cancel_requested_at=NULL,log_buffer_ciphertext=NULL WHERE deployment_id=?",
			owner,
		)
	}
	require.NoError(t, err)
	changed, err = repo.ReconcileDeploymentQueue(
		t.Context(),
		owner,
		db.CreateAuditLogParams{},
	)
	require.NoError(t, err)
	require.True(t, changed)
	active, err := repo.Queries.GetDeploymentSlot(t.Context(), next)
	require.NoError(t, err)
	require.Equal(t, next, active)
}

func TestQueueRecoveryRequiresLocalCleanupEvidence(t *testing.T) {
	for _, namespace := range []any{nil, "owned"} {
		t.Run(fmt.Sprint(namespace), func(t *testing.T) {
			repo, _ := openDeploymentCreationEngine(
				t,
				newDeploymentCreationEngine(t, "SQLite"),
			)
			owner, _ := queueRecoveryFixture(t, repo)
			require.NoError(
				t,
				repo.Queries.UpdateDeploymentStatus(
					t.Context(),
					db.UpdateDeploymentStatusParams{
						ID:     owner,
						Status: "failed",
					},
				),
			)
			changed, err := repo.ReconcileDeploymentQueue(
				t.Context(),
				owner,
				db.CreateAuditLogParams{},
			)
			require.ErrorIs(t, err, ErrQueueRecoveryBlocked)
			require.False(t, changed)
			_, err = repo.DB.ExecContext(
				t.Context(),
				`UPDATE deployments SET status='cleanup_unconfirmed',container_namespace=? WHERE id=?`,
				namespace,
				owner,
			)
			require.NoError(t, err)
			changed, err = repo.ReconcileDeploymentQueue(
				t.Context(),
				owner,
				db.CreateAuditLogParams{},
			)
			require.ErrorIs(t, err, ErrQueueRecoveryBlocked)
			require.False(t, changed)
			d, err := repo.Queries.GetDeployment(t.Context(), owner)
			require.NoError(t, err)
			require.False(t, d.CleanupConfirmedAt.Valid)
		})
	}
}

func TestQueueRecoveryAuditFailureRollsBack(t *testing.T) {
	repo, _ := openDeploymentCreationEngine(
		t,
		newDeploymentCreationEngine(t, "SQLite"),
	)
	owner, next := queueRecoveryFixture(t, repo)
	_, err := repo.DB.ExecContext(
		t.Context(),
		"UPDATE deployments SET cleanup_confirmed_at=1 WHERE id=?",
		owner,
	)
	require.NoError(t, err)
	require.NoError(
		t,
		repo.Queries.UpdateDeploymentStatus(
			t.Context(),
			db.UpdateDeploymentStatusParams{ID: owner, Status: "failed"},
		),
	)
	_, err = repo.DB.ExecContext(
		t.Context(),
		`CREATE TRIGGER reject_recovery_audit BEFORE INSERT ON audit_log
WHEN NEW.action='reconcile_deployment_queue' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`,
	)
	require.NoError(t, err)
	changed, err := repo.ReconcileDeploymentQueue(
		t.Context(),
		owner,
		db.CreateAuditLogParams{},
	)
	require.Error(t, err)
	require.False(t, changed)
	active, err := repo.Queries.GetDeploymentSlot(t.Context(), next)
	require.NoError(t, err)
	require.Equal(t, owner, active)
	d, err := repo.Queries.GetDeployment(context.Background(), next)
	require.NoError(t, err)
	require.Equal(t, "queued", d.Status)
}
