package repository

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestLifecycleRemovalPreservesReassignedGrant(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		first, err := repo.Queries.CreateLifecycle(t.Context(),
			db.CreateLifecycleParams{Name: "first"})
		if err != nil {
			t.Fatal(err)
		}
		second, err := repo.Queries.CreateLifecycle(t.Context(),
			db.CreateLifecycleParams{Name: "second"})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{first.ID, second.ID} {
			if err := repo.Queries.SetProjectLifecycle(t.Context(),
				db.SetProjectLifecycleParams{
					ID: 1, LifecycleID: sql.NullInt64{Int64: id, Valid: true},
				}); err != nil {
				t.Fatal(err)
			}
		}
		// Removal authorized for first must not clear the replacement grant.
		rows, err := repo.Queries.ClearProjectLifecycleIfAssigned(t.Context(),
			db.ClearProjectLifecycleIfAssignedParams{
				ID: 1, LifecycleID: sql.NullInt64{Int64: first.ID, Valid: true},
			})
		if err != nil || rows != 0 {
			t.Fatalf("stale removal: rows=%d, err=%v", rows, err)
		}
		project, err := repo.Queries.GetProject(t.Context(), 1)
		if err != nil || !project.LifecycleID.Valid ||
			project.LifecycleID.Int64 != second.ID {
			t.Fatalf("replacement grant lost: %+v, %v", project, err)
		}
		rows, err = repo.Queries.ClearProjectLifecycleIfAssigned(t.Context(),
			db.ClearProjectLifecycleIfAssignedParams{
				ID: 1, LifecycleID: project.LifecycleID,
			})
		if err != nil || rows != 1 {
			t.Fatalf("current removal: rows=%d, err=%v", rows, err)
		}
	})
}
