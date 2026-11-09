package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestStartupRecoveryAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		ctx := t.Context()
		release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
			ProjectID: 1, Version: "recovery", StepsJson: "[]",
		})
		require.NoError(t, err)
		deployment, err := repo.Queries.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: release.ID, EnvironmentID: 1, Status: "running",
				Note: sql.NullString{String: "Operator note", Valid: true},
			},
		)
		require.NoError(t, err)
		for _, rollback := range []bool{true, false} {
			sentinel := errors.New("rollback recovery")
			err := repo.WithQueueMaintenanceTx(
				ctx,
				func(ctx context.Context, q *db.Queries) error {
					rows, err := q.FailOrphanedDeployments(
						ctx,
						sql.NullInt64{Int64: 200, Valid: true},
					)
					if err != nil {
						return err
					}
					require.Len(t, rows, 1)
					if err := RecordStartupRecovery(
						ctx,
						q,
						rows,
						RecoveryOrphanFailed,
					); err != nil {
						return err
					}
					if rollback {
						return sentinel
					}
					return nil
				},
			)
			stored, readErr := repo.Queries.GetDeployment(ctx, deployment.ID)
			require.NoError(t, readErr)
			logs, readErr := repo.Queries.ListDeploymentLogsByDeployment(
				ctx,
				deployment.ID,
			)
			require.NoError(t, readErr)
			if rollback {
				require.ErrorIs(t, err, sentinel)
				require.Equal(t, "running", stored.Status)
				require.Equal(t, "Operator note", stored.Note.String)
				require.Empty(t, logs)
				continue
			}
			require.NoError(t, err)
			require.Equal(t, "failed", stored.Status)
			require.Equal(
				t,
				"Operator note\n"+RecoveryOrphanFailed,
				stored.Note.String,
			)
			require.Len(t, logs, 1)
			require.Equal(t, RecoveryOrphanFailed, logs[0].Line)
		}
		rows, err := repo.Queries.FailOrphanedDeployments(
			ctx,
			sql.NullInt64{Int64: 300, Valid: true},
		)
		require.NoError(t, err)
		require.Empty(t, rows)
		// Cleanup confirmation retains its namespace predicate and returned status.
		_, err = repo.DB.ExecContext(
			ctx,
			"UPDATE deployments SET status='cleanup_unconfirmed', container_namespace='owned' WHERE id=?",
			deployment.ID,
		)
		require.NoError(t, err)
		for index, namespace := range []string{"other", "owned", "owned"} {
			rows, err := repo.Queries.ConfirmContainerCleanup(
				ctx,
				db.ConfirmContainerCleanupParams{
					Now:       sql.NullInt64{Int64: 400, Valid: true},
					Namespace: sql.NullString{String: namespace, Valid: true},
				},
			)
			require.NoError(t, err)
			if index == 1 {
				require.Len(t, rows, 1)
				require.Equal(t, "failed", rows[0].Status)
				require.False(t, rows[0].ContainerNamespace.Valid)
			} else {
				require.Empty(t, rows)
			}
		}
		_, err = repo.DB.ExecContext(
			ctx,
			"INSERT INTO deployment_steps(deployment_id,step_index,name,script_body) VALUES(?,0,'local','true')",
			deployment.ID,
		)
		require.NoError(t, err)
		_, err = repo.DB.ExecContext(
			ctx,
			"UPDATE deployments SET status='running',container_namespace='owned' WHERE id=?",
			deployment.ID,
		)
		require.NoError(t, err)
		rows, err = repo.Queries.MarkUnreconciledLocalDeployments(ctx,
			sql.NullInt64{Int64: 500, Valid: true})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, "cleanup_unconfirmed", rows[0].Status)
		require.Equal(t, "owned", rows[0].ContainerNamespace.String)
	})
}

func TestStartupRecoveryLogFailureRollsBack(t *testing.T) {
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	ctx := t.Context()
	d, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "running",
	})
	require.NoError(t, err)
	_, err = repo.DB.ExecContext(ctx, `CREATE TRIGGER reject_recovery_log
BEFORE INSERT ON deployment_logs BEGIN SELECT RAISE(ABORT, 'log failed'); END`)
	require.NoError(t, err)
	err = repo.WithQueueMaintenanceTx(
		ctx,
		func(ctx context.Context, q *db.Queries) error {
			rows, err := q.FailOrphanedDeployments(
				ctx,
				sql.NullInt64{Int64: 200, Valid: true},
			)
			if err != nil {
				return err
			}
			return RecordStartupRecovery(ctx, q, rows, RecoveryOrphanFailed)
		},
	)
	require.ErrorContains(t, err, "log failed")
	stored, err := repo.Queries.GetDeployment(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, "running", stored.Status)
	require.False(t, stored.Note.Valid)
}
