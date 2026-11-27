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

type projectArtifactSource struct {
	selected db.PackageRepository
	source   artifact.Repository
	url      string
	attached bool
}

func (r *Repository) prepareProjectArtifactSource(
	ctx context.Context,
	projectID int64,
	version string,
) (prepared projectArtifactSource, err error) {
	prepared.selected, err = r.Queries.GetProjectArtifactRepository(
		ctx,
		projectID,
	)
	if err != nil {
		return prepared, err
	}
	prepared.attached = true
	source, err := r.GetPackageRepository(ctx, prepared.selected.ID)
	if err != nil {
		return prepared, err
	}
	prepared.source = PackageSource(source)
	prepared.url, err = prepared.source.URL(version)
	return prepared, err
}

func (r *Repository) ResolveProjectArtifact(
	ctx context.Context,
	projectID int64,
	version string,
) (*ArtifactSnapshot, error) {
	prepared, err := r.prepareProjectArtifactSource(ctx, projectID, version)
	if !prepared.attached && errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	download, err := r.ArtifactClient.Fetch(
		ctx,
		prepared.source,
		artifact.Pin{URL: prepared.url, Version: version},
	)
	if errors.Is(err, artifact.ErrNotFound) {
		return nil, &PackageMissingError{
			Version: version,
			Source:  prepared.selected,
		}
	}
	if err != nil {
		return nil, err
	}
	if err := os.Remove(download.Path); err != nil {
		return nil, err
	}
	return &ArtifactSnapshot{
		Source: prepared.selected,
		Row: db.CreateReleaseArtifactParams{
			RepositoryID: prepared.selected.ID,
			Url:          prepared.url,
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
	download, err := r.ArtifactClient.Fetch(
		ctx,
		PackageSource(source),
		artifact.Pin{
			URL:     row.Url,
			Version: row.Version,
			SHA256:  row.Sha256,
			Size:    row.Size,
		},
	)
	if err != nil || row.Sha256 != "" {
		return download, err
	}
	if err := r.resolveDeploymentArtifact(ctx, row, download); err != nil {
		return artifact.Download{}, errors.Join(err, os.Remove(download.Path))
	}
	return download, nil
}
