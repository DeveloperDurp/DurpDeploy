package repository

import (
	"database/sql"
	"math"
	"testing"

	"durpdeploy/internal/db"
)

func TestLogPagesAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		deployment, err := repo.Queries.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID:     1,
				EnvironmentID: 1,
				Status:        "running",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		// Given: state metadata is older than the current output window.
		var state db.DeploymentLog
		err = repo.WithTx(t.Context(), func(q *db.Queries) error {
			var err error
			state, err = q.CreateStepDeploymentLog(t.Context(),
				db.CreateStepDeploymentLogParams{
					DeploymentID: deployment.ID,
					Line:         "start",
					StepIndex:    sql.NullInt64{Int64: 0, Valid: true},
					StepName:     sql.NullString{String: "step", Valid: true},
					StepState: sql.NullString{
						String: "running",
						Valid:  true,
					},
				})
			if err != nil {
				return err
			}
			for range 501 {
				if _, err := q.CreateStepDeploymentLog(
					t.Context(),
					db.CreateStepDeploymentLogParams{
						DeploymentID: deployment.ID,
						Line:         "output",
						StepIndex:    state.StepIndex,
						StepName:     state.StepName,
					},
				); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		// When: newest and older pages are read through the database adapter.
		page, err := repo.ReadLogPage(t.Context(), deployment.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		older, err := repo.ReadLogPage(
			t.Context(),
			deployment.ID,
			page.NextBefore,
		)
		if err != nil {
			t.Fatal(err)
		}
		// Then: pages do not overlap and metadata preserves the older state.
		if len(page.Items) != 500 || len(older.Items) != 2 ||
			older.NextBefore != 0 || older.LastID >= page.Items[0].ID {
			t.Fatal("invalid page boundaries")
		}
		for _, cursor := range []int64{math.MaxInt32 + 1, math.MaxInt64} {
			large, err := repo.ReadLogPage(t.Context(), deployment.ID, cursor)
			if err != nil {
				t.Fatalf("read 64-bit cursor %d: %v", cursor, err)
			}
			if len(large.Items) != 500 || large.LastID != page.LastID {
				t.Fatalf("incorrect page for 64-bit cursor %d", cursor)
			}
		}
		states, err := repo.Queries.ListDeploymentLogStateMetadata(
			t.Context(),
			db.ListDeploymentLogStateMetadataParams{
				DeploymentID: deployment.ID,
				LastID:       page.LastID,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != 1 || states[0].StepState.String != "running" ||
			states[0].ID != state.ID {
			t.Fatalf("state metadata = %+v", states)
		}
	})
}
