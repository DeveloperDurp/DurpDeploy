package repository

import (
	"context"

	"durpdeploy/internal/db"
)

func (r *Repository) ListEnvironmentsForDisplay(
	ctx context.Context,
	arg db.ListEnvironmentsPaginatedParams,
	includeVerification bool,
) ([]db.Environment, error) {
	if includeVerification {
		return r.ListEnvironmentsPaginated(ctx, arg)
	}
	rows, err := r.Queries.ListEnvironmentsPaginated(ctx, arg)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].VerificationTarget = ""
	}
	return rows, nil
}

func (r *Repository) GetEnvironmentForDisplay(
	ctx context.Context,
	id int64,
	includeVerification bool,
) (db.Environment, error) {
	if includeVerification {
		return r.GetEnvironment(ctx, id)
	}
	row, err := r.Queries.GetEnvironment(ctx, id)
	row.VerificationTarget = ""
	return row, err
}
