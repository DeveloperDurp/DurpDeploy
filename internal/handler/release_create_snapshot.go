package handler

import (
	"context"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func CreateReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
	version string,
) (db.Release, error) {
	snapshot, err := buildReleaseSnapshot(ctx, repo, projectID)
	if err != nil {
		return db.Release{}, err
	}
	snapshot.artifact, err = repo.CaptureProjectArtifact(
		ctx, projectID, version,
	)
	if err != nil {
		return db.Release{}, err
	}
	if snapshot.artifact != nil && snapshot.artifact.Row.RepositoryID != 0 &&
		snapshot.hasAgentSteps {
		return db.Release{}, repository.ErrRemoteArtifactsUnsupported
	}
	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		return db.Release{}, err
	}
	defer tx.Rollback()
	queries := repo.Queries.WithTx(tx)
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: projectID, Version: version,
		StepsJson: snapshot.stepsJSON,
	})
	if err != nil {
		return db.Release{}, err
	}
	if err := snapshot.insertVariables(ctx, queries, release.ID); err != nil {
		return db.Release{}, err
	}
	if err := snapshot.artifact.Insert(ctx, queries, release.ID); err != nil {
		return db.Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return db.Release{}, err
	}
	return release, nil
}
