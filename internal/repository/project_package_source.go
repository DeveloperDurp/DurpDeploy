package repository

import (
	"context"

	"durpdeploy/internal/db"
	"github.com/google/uuid"
)

func writeProjectPackageSource(
	ctx context.Context,
	q *db.Queries,
	source db.CreatePackageRepositoryParams,
) (db.PackageRepository, error) {
	candidates, err := q.ListPackageRepositoriesBySource(
		ctx,
		db.ListPackageRepositoriesBySourceParams{
			ProjectID:   source.ProjectID,
			UrlTemplate: source.UrlTemplate,
			AuthType:    source.AuthType,
			Username:    source.Username,
		},
	)
	if err != nil {
		return db.PackageRepository{}, err
	}
	for _, retained := range candidates {
		// SQL Server collation may ignore case or trailing spaces. Source identity
		// must be byte-exact before historical credentials or metadata can change.
		if retained.UrlTemplate != source.UrlTemplate ||
			retained.AuthType != source.AuthType ||
			retained.Username != source.Username {
			continue
		}
		if _, err := q.LockPackageRepository(ctx, retained.ID); err != nil {
			return db.PackageRepository{}, err
		}
		return q.UpdatePackageRepository(ctx, db.UpdatePackageRepositoryParams{
			ID:          retained.ID,
			ProjectID:   source.ProjectID,
			Name:        retained.Name,
			UrlTemplate: source.UrlTemplate,
			AuthType:    source.AuthType,
			Username:    source.Username,
			Credential:  source.Credential,
		})
	}
	source.Name = uuid.NewString()
	return q.CreatePackageRepository(ctx, source)
}
