package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

var ErrArtifactRepositoryPinned = errors.New(
	"repository is referenced by artifact pins",
)

var ErrRemoteArtifactsUnsupported = errors.New(
	"artifact packages require local steps until agent support is available",
)

var ErrArtifactPathReserved = errors.New(
	"ARTIFACT_PATH is reserved for deployment artifacts",
)

func PackageSource(row db.PackageRepository) artifact.Repository {
	return artifact.Repository{
		URLTemplate: row.UrlTemplate,
		AuthType:    row.AuthType,
		Username:    row.Username,
		Credential:  row.Credential,
	}
}

func (r *Repository) GetPackageRepository(
	ctx context.Context,
	id int64,
) (db.PackageRepository, error) {
	row, err := r.Queries.GetPackageRepository(ctx, id)
	if err != nil {
		return row, err
	}
	value, err := r.decryptValue(
		sql.NullString{String: row.Credential, Valid: row.Credential != ""},
	)
	row.Credential = value.String
	return row, err
}

func (r *Repository) CreatePackageRepository(
	ctx context.Context,
	arg db.CreatePackageRepositoryParams,
) (db.PackageRepository, error) {
	if strings.TrimSpace(arg.Name) == "" || len(arg.Name) > 255 {
		return db.PackageRepository{}, artifact.ErrInvalid
	}
	source := artifact.Repository{
		URLTemplate: arg.UrlTemplate,
		AuthType:    arg.AuthType,
		Username:    arg.Username,
		Credential:  arg.Credential,
	}
	if err := source.Validate(); err != nil {
		return db.PackageRepository{}, err
	}
	if source.Credential != "" && r.secrets == nil {
		return db.PackageRepository{}, errors.New(
			"repository credentials require secret encryption",
		)
	}
	value, err := r.encryptValue(
		sql.NullString{String: arg.Credential, Valid: arg.Credential != ""},
	)
	if err != nil {
		return db.PackageRepository{}, err
	}
	arg.Credential = value.String
	return r.Queries.CreatePackageRepository(ctx, arg)
}

func (r *Repository) UpdatePackageRepository(
	ctx context.Context,
	arg db.UpdatePackageRepositoryParams,
) (row db.PackageRepository, err error) {
	err = r.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockPackageRepository(ctx, arg.ID); err != nil {
			return err
		}
		old, err := q.GetPackageRepository(ctx, arg.ID)
		if err != nil {
			return err
		}
		if old.ProjectID != arg.ProjectID {
			return sql.ErrNoRows
		}
		pins, err := q.PackageRepositoryHasPins(
			ctx,
			db.PackageRepositoryHasPinsParams{
				RepositoryID:   arg.ID,
				RepositoryID_2: arg.ID,
			},
		)
		if err != nil {
			return err
		}
		if pins != 0 &&
			(arg.UrlTemplate != old.UrlTemplate || arg.AuthType != old.AuthType || arg.Username != old.Username) {
			return ErrArtifactRepositoryPinned
		}
		value, err := r.decryptValue(
			sql.NullString{String: old.Credential, Valid: old.Credential != ""},
		)
		if err != nil {
			return err
		}
		credential := arg.Credential
		if credential == "" && arg.AuthType == old.AuthType &&
			arg.AuthType != "noauth" {
			credential = value.String
		}
		if strings.TrimSpace(arg.Name) == "" || len(arg.Name) > 255 {
			return artifact.ErrInvalid
		}
		source := artifact.Repository{
			URLTemplate: arg.UrlTemplate,
			AuthType:    arg.AuthType,
			Username:    arg.Username,
			Credential:  credential,
		}
		if err := source.Validate(); err != nil {
			return err
		}
		if credential != "" && r.secrets == nil {
			return errors.New(
				"repository credentials require secret encryption",
			)
		}
		if arg.Credential == "" && arg.AuthType == old.AuthType {
			row, err = q.UpdatePackageRepositoryKeepCredential(
				ctx,
				db.UpdatePackageRepositoryKeepCredentialParams{
					ID:          arg.ID,
					ProjectID:   arg.ProjectID,
					Name:        arg.Name,
					UrlTemplate: arg.UrlTemplate,
					AuthType:    arg.AuthType,
					Username:    arg.Username,
				},
			)
			return err
		} else {
			encrypted, err := r.encryptValue(
				sql.NullString{String: credential, Valid: credential != ""},
			)
			if err != nil {
				return err
			}
			arg.Credential = encrypted.String
		}
		row, err = q.UpdatePackageRepository(ctx, arg)
		return err
	})
	return row, err
}

func (r *Repository) SelectArtifactRepository(
	ctx context.Context,
	arg db.SelectProjectArtifactRepositoryParams,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		if arg.RepositoryID != 0 {
			row, err := q.GetPackageRepository(ctx, arg.RepositoryID)
			if err != nil {
				return err
			}
			if row.ProjectID != arg.ProjectID {
				return sql.ErrNoRows
			}
		}
		if err := q.DeleteProjectArtifactRepository(
			ctx,
			arg.ProjectID,
		); err != nil {
			return err
		}
		if arg.RepositoryID == 0 {
			return nil
		}
		return q.SelectProjectArtifactRepository(ctx, arg)
	})
}

func (r *Repository) DeletePackageRepository(
	ctx context.Context,
	arg db.DeletePackageRepositoryParams,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		row, err := q.GetPackageRepository(ctx, arg.ID)
		if err != nil {
			return err
		}
		if row.ProjectID != arg.ProjectID {
			return sql.ErrNoRows
		}
		pins, err := q.PackageRepositoryHasPins(
			ctx,
			db.PackageRepositoryHasPinsParams{
				RepositoryID:   arg.ID,
				RepositoryID_2: arg.ID,
			},
		)
		if err != nil {
			return err
		}
		if pins != 0 {
			return ErrArtifactRepositoryPinned
		}
		selected, err := q.GetProjectArtifactRepository(ctx, arg.ProjectID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if selected.ID == arg.ID {
			if err := q.DeleteProjectArtifactRepository(
				ctx,
				arg.ProjectID,
			); err != nil {
				return err
			}
		}
		return q.DeletePackageRepository(ctx, arg)
	})
}
