package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

func (r *Repository) SaveProjectPackageRepository(
	ctx context.Context,
	projectID int64,
	source artifact.Repository,
) (row db.PackageRepository, err error) {
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			locked, err := q.LockProject(ctx, projectID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			old, err := q.GetProjectArtifactRepository(ctx, projectID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			sameSource := old.ID != 0 &&
				old.UrlTemplate == source.URLTemplate &&
				old.AuthType == source.AuthType &&
				old.Username == source.Username
			credential := source.Credential
			if sameSource && credential == "" && source.AuthType != "noauth" {
				value, err := r.decryptValue(
					sql.NullString{
						String: old.Credential,
						Valid:  old.Credential != "",
					},
				)
				if err != nil {
					return err
				}
				credential = value.String
			}
			validated := source
			validated.Credential = credential
			if err := validated.Validate(); err != nil {
				return err
			}
			if credential != "" && r.secrets == nil {
				return errors.New(
					"repository credentials require secret encryption",
				)
			}
			encrypted, err := r.encryptValue(
				sql.NullString{String: credential, Valid: credential != ""},
			)
			if err != nil {
				return err
			}
			row, err = writeProjectPackageSource(
				ctx,
				q,
				db.CreatePackageRepositoryParams{
					ProjectID:   projectID,
					UrlTemplate: source.URLTemplate,
					AuthType:    source.AuthType,
					Username:    source.Username,
					Credential:  encrypted.String,
				},
			)
			if err != nil {
				return err
			}
			if row.ID == old.ID {
				return nil
			}
			if err := q.DeleteProjectArtifactRepository(
				ctx,
				projectID,
			); err != nil {
				return err
			}
			if err := q.SelectProjectArtifactRepository(
				ctx,
				db.SelectProjectArtifactRepositoryParams{
					ProjectID:    projectID,
					RepositoryID: row.ID,
				},
			); err != nil {
				return err
			}
			return removeUnpinnedPackageSource(ctx, q, old)
		})
	})
	return row, err
}

func (r *Repository) RemoveProjectPackageRepository(
	ctx context.Context,
	projectID int64,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			locked, err := q.LockProject(ctx, projectID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			old, err := q.GetProjectArtifactRepository(ctx, projectID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := q.DeleteProjectArtifactRepository(
				ctx,
				projectID,
			); err != nil {
				return err
			}
			return removeUnpinnedPackageSource(ctx, q, old)
		})
	})
}

func removeUnpinnedPackageSource(
	ctx context.Context,
	q *db.Queries,
	source db.PackageRepository,
) error {
	if source.ID == 0 {
		return nil
	}
	if _, err := q.LockPackageRepository(ctx, source.ID); err != nil {
		return err
	}
	pins, err := q.PackageRepositoryHasPins(
		ctx,
		db.PackageRepositoryHasPinsParams{
			RepositoryID:   source.ID,
			RepositoryID_2: source.ID,
		},
	)
	if err != nil {
		return err
	}
	if pins != 0 {
		return nil
	}
	return q.DeletePackageRepository(
		ctx,
		db.DeletePackageRepositoryParams{
			ProjectID: source.ProjectID,
			ID:        source.ID,
		},
	)
}

func (r *Repository) TestProjectPackage(
	ctx context.Context,
	projectID int64,
	version string,
) (artifact.Pin, error) {
	selected, err := r.Queries.GetProjectArtifactRepository(ctx, projectID)
	if err != nil {
		return artifact.Pin{}, err
	}
	source, err := r.GetPackageRepository(ctx, selected.ID)
	if err != nil {
		return artifact.Pin{}, err
	}
	url, err := PackageSource(source).URL(version)
	if err != nil {
		return artifact.Pin{}, err
	}
	download, err := r.ArtifactClient.Fetch(
		ctx,
		PackageSource(source),
		artifact.Pin{URL: url, Version: version},
	)
	if err != nil {
		return artifact.Pin{}, err
	}
	if err := os.Remove(download.Path); err != nil {
		return artifact.Pin{}, err
	}
	return artifact.Pin{
		URL:     url,
		Version: version,
		SHA256:  download.SHA256,
		Size:    download.Size,
	}, nil
}
