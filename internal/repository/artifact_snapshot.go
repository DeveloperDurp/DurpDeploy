package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

type ArtifactSnapshot struct {
	Row    db.CreateReleaseArtifactParams
	Source db.PackageRepository
}

func (r *Repository) ResolveProjectArtifact(
	ctx context.Context,
	projectID int64,
	version string,
) (*ArtifactSnapshot, error) {
	selected, err := r.Queries.GetProjectArtifactRepository(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	source, err := r.GetPackageRepository(ctx, selected.ID)
	if err != nil {
		return nil, err
	}
	url, err := PackageSource(source).URL(version)
	if err != nil {
		return nil, err
	}
	download, err := r.ArtifactClient.Fetch(
		ctx,
		PackageSource(source),
		artifact.Pin{URL: url, Version: version},
	)
	if errors.Is(err, artifact.ErrNotFound) {
		return nil, &PackageMissingError{Version: version, Source: selected}
	}
	if err != nil {
		return nil, err
	}
	if err := os.Remove(download.Path); err != nil {
		return nil, err
	}
	return &ArtifactSnapshot{
		Source: selected,
		Row: db.CreateReleaseArtifactParams{
			RepositoryID: selected.ID,
			Url:          url,
			Version:      version,
			Sha256:       download.SHA256,
			Size:         download.Size,
		},
	}, nil
}

// Insert checks that configuration did not change while the ZIP was resolving.
func (s *ArtifactSnapshot) Insert(
	ctx context.Context,
	q *db.Queries,
	releaseID int64,
) error {
	if s == nil {
		return nil
	}
	if _, err := q.LockPackageRepository(ctx, s.Source.ID); err != nil {
		return err
	}
	current, err := q.GetProjectArtifactRepository(ctx, s.Source.ProjectID)
	if err != nil {
		return err
	}
	if current.ID != s.Source.ID ||
		current.UrlTemplate != s.Source.UrlTemplate ||
		current.AuthType != s.Source.AuthType ||
		current.Username != s.Source.Username {
		return ErrArtifactRepositoryPinned
	}
	row := s.Row
	row.ReleaseID = releaseID
	if row.RepositoryID == 0 {
		return nil // An explicitly omitted package has no artifact pin.
	}
	return q.CreateReleaseArtifact(ctx, row)
}

func ValidateArtifactSteps(steps []DeploymentStepSnapshot) error {
	for _, step := range steps {
		if step.ExecutionTarget == "agent" {
			return ErrRemoteArtifactsUnsupported
		}
	}
	return nil
}

func validateDeploymentArtifact(
	ctx context.Context,
	q *db.Queries,
	deploymentID int64,
	steps []DeploymentStepSnapshot,
) error {
	_, err := q.GetDeploymentArtifact(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ValidateArtifactSteps(steps)
}

func (r *Repository) DownloadDeploymentArtifact(
	ctx context.Context,
	deploymentID int64,
) (artifact.Download, error) {
	row, err := r.Queries.GetDeploymentArtifact(ctx, deploymentID)
	if err != nil {
		return artifact.Download{}, err
	}
	source, err := r.GetPackageRepository(ctx, row.RepositoryID)
	if err != nil {
		return artifact.Download{}, err
	}
	return r.ArtifactClient.Fetch(
		ctx,
		PackageSource(source),
		artifact.Pin{
			URL:     row.Url,
			Version: row.Version,
			SHA256:  row.Sha256,
			Size:    row.Size,
		},
	)
}
