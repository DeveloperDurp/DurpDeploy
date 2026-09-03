package handler

import (
	"context"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// CreateReleaseSnapshot snapshots the project's current steps and variables
// into a new release. Exported so the JSON API handler can reuse the same
// transaction logic as the web form handler.
func CreateReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
	version string,
) (db.Release, error) {
	steps, err := repo.Queries.ListStepsByProject(ctx, projectID)
	if err != nil {
		return db.Release{}, err
	}

	stepsJSON, err := BuildReleaseStepsJSON(steps)
	if err != nil {
		return db.Release{}, err
	}

	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		return db.Release{}, err
	}
	defer tx.Rollback()

	qtx := repo.Queries.WithTx(tx)

	release, err := qtx.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: projectID,
		Version:   version,
		StepsJson: stepsJSON,
	})
	if err != nil {
		return db.Release{}, err
	}

	variables, err := repo.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return db.Release{}, err
	}

	for _, variable := range variables {
		encValue, err := repo.EncryptValue(variable.Value)
		if err != nil {
			return db.Release{}, err
		}
		if _, err := qtx.CreateReleaseVariable(
			ctx,
			db.CreateReleaseVariableParams{
				ReleaseID:     release.ID,
				Name:          variable.Name,
				Value:         encValue,
				EnvironmentID: variable.EnvironmentID,
				Secret:        variable.Secret,
			},
		); err != nil {
			return db.Release{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return db.Release{}, err
	}
	return release, nil
}
