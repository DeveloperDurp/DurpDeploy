package repository

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestDeploymentLogOrderAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		ctx := t.Context()
		release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
			ProjectID: 1, Version: "log-order",
			StepsJson: `[{"name":"step","container_image":"bash:5.2"}]`,
		})
		require.NoError(t, err)
		created, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: 1, Status: "pending",
		})
		require.NoError(t, err)
		id := created.Deployment.ID
		_, err = repo.Queries.CreateDeploymentStepAttempt(ctx,
			db.CreateDeploymentStepAttemptParams{DeploymentID: id, StepIndex: 0,
				Attempt: 1, WaitDeadline: 1000, Now: 100})
		require.NoError(t, err)
		var ids []int64
		for range 6 {
			row, err := repo.Queries.CreateRemoteDeploymentLog(
				ctx,
				db.CreateRemoteDeploymentLogParams{
					DeploymentID: id,
					CreatedAt:    100,
					Line:         "tie",
				},
			)
			require.NoError(t, err)
			ids = append(ids, row.ID)
		}
		// Reverse insertion and producer sequence to distinguish both contracts.
		for sequence, index := range []int{3, 2} {
			require.NoError(t, repo.Queries.CreateDeploymentLogScope(ctx,
				db.CreateDeploymentLogScopeParams{DeploymentID: id,
					LogID: ids[index], Sequence: int64(sequence)}))
		}
		for sequence, index := range []int{4, 5} {
			require.NoError(t, repo.Queries.CreateDeploymentLogScope(ctx,
				db.CreateDeploymentLogScopeParams{DeploymentID: id,
					LogID: ids[index], Sequence: int64(sequence),
					StepIndex: sql.NullInt64{Int64: 0, Valid: true},
					Attempt:   sql.NullInt64{Int64: 1, Valid: true}}))
		}
		// Timestamps outrank IDs within each timestamp-ordered group.
		_, err = repo.DB.ExecContext(
			ctx,
			"UPDATE deployment_logs SET created_at=200 WHERE id IN (?, ?)",
			ids[0],
			ids[4],
		)
		require.NoError(t, err)
		want := []int64{ids[0], ids[1], ids[3], ids[2], ids[4], ids[5]}
		for range 3 {
			rows, err := repo.Queries.ListDeploymentLogsByDeployment(ctx, id)
			require.NoError(t, err)
			var got []int64
			for _, row := range rows {
				got = append(got, row.ID)
			}
			require.Equal(t, want, got)
		}
		_, err = repo.DB.ExecContext(
			ctx,
			"UPDATE deployment_logs SET created_at=100 WHERE deployment_id=?",
			id,
		)
		require.NoError(t, err)
		rows, err := repo.Queries.ListDeploymentLogsByDeployment(ctx, id)
		require.NoError(t, err)
		var got []int64
		for _, row := range rows {
			got = append(got, row.ID)
		}
		require.Equal(
			t,
			[]int64{ids[1], ids[0], ids[3], ids[2], ids[5], ids[4]},
			got,
		)
	})
}
