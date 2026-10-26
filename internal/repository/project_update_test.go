package repository

import (
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectUpdateAtomic(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		original, err := repo.Queries.GetProject(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		lifecycle, err := repo.Queries.CreateLifecycle(t.Context(),
			db.CreateLifecycleParams{Name: "atomic"})
		if err != nil {
			t.Fatal(err)
		}
		params := db.UpdateProjectParams{
			ID: original.ID, Name: "edited",
			Description: sql.NullString{
				String: "edited description",
				Valid:  true,
			},
		}
		setLifecycle := func(txRepo *Repository) error {
			return txRepo.Queries.SetProjectLifecycle(t.Context(),
				db.SetProjectLifecycleParams{
					ID: original.ID,
					LifecycleID: sql.NullInt64{
						Int64: lifecycle.ID,
						Valid: true,
					},
				})
		}
		failure := errors.New("lifecycle publication failed")
		_, err = repo.UpdateProject(t.Context(), params,
			func(txRepo *Repository) error {
				if err := setLifecycle(txRepo); err != nil {
					return err
				}
				return failure
			})
		if !errors.Is(err, failure) {
			t.Fatalf("update error: %v", err)
		}
		actual, err := repo.Queries.GetProject(t.Context(), original.ID)
		if err != nil || actual != original {
			t.Fatalf("partial update survived: %+v, %v", actual, err)
		}
		updated, err := repo.UpdateProject(t.Context(), params, setLifecycle)
		if err != nil || updated.Name != params.Name ||
			updated.Description != params.Description ||
			!updated.LifecycleID.Valid || updated.LifecycleID.Int64 != lifecycle.ID {
			t.Fatalf("committed update: %+v, %v", updated, err)
		}
		stored, err := repo.Queries.GetProject(t.Context(), original.ID)
		if err != nil || stored != updated {
			t.Fatalf("response differs from commit: %+v, %v", stored, err)
		}
	})
}
