package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

func (r *Repository) DecryptVerificationTarget(value string) (string, error) {
	plain, err := r.decryptValue(sql.NullString{String: value, Valid: true})
	return plain.String, err
}

func (r *Repository) decryptEnvironment(
	env db.Environment,
) (db.Environment, error) {
	target, err := r.DecryptVerificationTarget(env.VerificationTarget)
	if err != nil {
		return db.Environment{}, err
	}
	env.VerificationTarget = target
	return env, nil
}

func (r *Repository) CreateEnvironment(
	ctx context.Context, arg db.CreateEnvironmentParams,
) (db.Environment, error) {
	target, err := r.encryptValue(sql.NullString{
		String: arg.VerificationTarget, Valid: true,
	})
	if err != nil {
		return db.Environment{}, err
	}
	arg.VerificationTarget = target.String
	env, err := r.Queries.CreateEnvironment(ctx, arg)
	if err != nil {
		return db.Environment{}, err
	}
	return r.decryptEnvironment(env)
}

func (r *Repository) UpdateEnvironment(
	ctx context.Context, arg db.UpdateEnvironmentParams,
) (db.Environment, error) {
	target, err := r.encryptValue(sql.NullString{
		String: arg.VerificationTarget, Valid: true,
	})
	if err != nil {
		return db.Environment{}, err
	}
	arg.VerificationTarget = target.String
	env, err := r.Queries.UpdateEnvironment(ctx, arg)
	if err != nil {
		return db.Environment{}, err
	}
	return r.decryptEnvironment(env)
}

func (r *Repository) GetEnvironment(
	ctx context.Context, id int64,
) (db.Environment, error) {
	env, err := r.Queries.GetEnvironment(ctx, id)
	if err != nil {
		return db.Environment{}, err
	}
	return r.decryptEnvironment(env)
}

func (r *Repository) ListEnvironmentsPaginated(
	ctx context.Context, arg db.ListEnvironmentsPaginatedParams,
) ([]db.Environment, error) {
	rows, err := r.Queries.ListEnvironmentsPaginated(ctx, arg)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i], err = r.decryptEnvironment(rows[i])
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
