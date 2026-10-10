package handler

import (
	"context"
	"errors"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type ReleaseSnapshotRequest struct {
	ProjectID           int64
	Version             string
	AllowMissingPackage bool
}

// CreateReleaseSnapshot is the strict path shared by existing callers.
func CreateReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
	version string,
) (db.Release, error) {
	return CreateReleaseSnapshotWithPackage(ctx, repo, ReleaseSnapshotRequest{
		ProjectID: projectID, Version: version,
	})
}

func CreateReleaseSnapshotWithPackage(
	ctx context.Context,
	repo *repository.Repository,
	request ReleaseSnapshotRequest,
) (db.Release, error) {
	snapshot, err := buildReleaseSnapshot(ctx, repo, request.ProjectID)
	if err != nil {
		return db.Release{}, err
	}
	snapshot.artifact, err = repo.ResolveProjectArtifact(
		ctx, request.ProjectID, request.Version,
	)
	var missing *repository.PackageMissingError
	packageOmitted := int64(0)
	if request.AllowMissingPackage && errors.As(err, &missing) {
		// Keep the source identity so Insert checks concurrent source edits.
		snapshot.artifact = &repository.ArtifactSnapshot{Source: missing.Source}
		packageOmitted = 1
	} else if err != nil {
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
		ProjectID: request.ProjectID, Version: request.Version,
		StepsJson: snapshot.stepsJSON, PackageOmitted: packageOmitted,
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
