package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

// CaptureProjectArtifact freezes the attachment without contacting its server.
func (r *Repository) CaptureProjectArtifact(
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
	return &ArtifactSnapshot{Source: selected,
		Row: db.CreateReleaseArtifactParams{
			RepositoryID: selected.ID, Url: url, Version: version,
			ResolutionKey: uuid.NewString(),
		}}, nil
}

func (r *Repository) resolveDeploymentArtifact(
	ctx context.Context,
	requested db.DeploymentArtifact,
	download artifact.Download,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			deployment, err := q.GetDeployment(ctx, requested.DeploymentID)
			if err != nil {
				return err
			}
			if err := lockRelease(ctx, q, deployment.ReleaseID); err != nil {
				return err
			}
			current, err := q.GetDeploymentArtifact(ctx, requested.DeploymentID)
			if err != nil {
				return err
			}
			if current.ResolutionKey == "" ||
				current.ResolutionKey != requested.ResolutionKey ||
				current.RepositoryID != requested.RepositoryID ||
				current.Url != requested.Url || current.Version != requested.Version {
				return ErrArtifactRepositoryPinned
			}
			if current.Sha256 != "" {
				if current.Sha256 != download.SHA256 ||
					current.Size != download.Size {
					return artifact.ErrChecksum
				}
				return nil
			}
			if err := q.ResolveReleaseArtifacts(ctx,
				db.ResolveReleaseArtifactsParams{
					Sha256: download.SHA256, Size: download.Size,
					ResolutionKey: current.ResolutionKey,
				}); err != nil {
				return err
			}
			return q.ResolveDeploymentArtifacts(ctx,
				db.ResolveDeploymentArtifactsParams{
					Sha256: download.SHA256, Size: download.Size,
					ResolutionKey: current.ResolutionKey,
				})
		})
	})
}
