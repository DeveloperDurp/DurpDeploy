package handler

import (
	"context"

	"durpdeploy/internal/db"
	"durpdeploy/internal/provenance"
	"durpdeploy/internal/repository"
)

func CreateReleaseSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
	version string,
) (db.Release, error) {
	return CreateReleaseWithProvenance(ctx, repo, projectID, version,
		provenance.Source{}, repository.IdempotencyRequest{})
}

func CreateReleaseWithProvenance(
	ctx context.Context, repo *repository.Repository,
	projectID int64, version string, source provenance.Source,
	request repository.IdempotencyRequest,
) (db.Release, error) {
	if err := source.ValidateWithScrubber(repo.NewScrubber(nil)); err != nil {
		return db.Release{}, err
	}
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
	var release db.Release
	err = repo.WithCreationTx(ctx, func(queries *db.Queries) error {
		if err := request.Claim(ctx, queries); err != nil {
			return err
		}
		var err error
		release, err = queries.CreateReleaseWithProvenance(ctx,
			db.CreateReleaseWithProvenanceParams{
				ProjectID: projectID, Version: version,
				StepsJson:     snapshot.stepsJSON,
				RepositoryUrl: source.RepositoryURL, CommitSha: source.CommitSHA,
				ArtifactRef: source.ArtifactRef, CiRunUrl: source.CIRunURL,
			})
		if err != nil {
			return err
		}
		if err := snapshot.insertVariables(ctx, queries, release.ID); err != nil {
			return err
		}
		if err := snapshot.artifact.Insert(ctx, queries, release.ID); err != nil {
			return err
		}
		return request.Complete(ctx, queries, release)
	})
	return release, err
}
