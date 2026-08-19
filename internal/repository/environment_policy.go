package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var ErrEnvironmentPolicyPool = errors.New("select an enabled agent pool")

type EnvironmentPolicy struct {
	Remote   bool
	PoolID   int64
	Selector string
}

type EnvironmentPolicyChange struct {
	EnvironmentID int64
	Policy        EnvironmentPolicy
}

func (r *Repository) CreateEnvironmentWithPolicy(
	ctx context.Context,
	params db.CreateEnvironmentParams,
	policy EnvironmentPolicy,
) error {
	return r.WithTx(ctx, func(queries *db.Queries) error {
		environment, err := queries.CreateEnvironment(ctx, params)
		if err != nil {
			return err
		}
		return saveEnvironmentPolicy(ctx, queries, EnvironmentPolicyChange{
			EnvironmentID: environment.ID,
			Policy:        policy,
		})
	})
}

func (r *Repository) UpdateEnvironmentWithPolicy(
	ctx context.Context,
	params db.UpdateEnvironmentParams,
	policy EnvironmentPolicy,
) error {
	return r.WithTx(ctx, func(queries *db.Queries) error {
		if _, err := queries.UpdateEnvironment(ctx, params); err != nil {
			return err
		}
		return saveEnvironmentPolicy(ctx, queries, EnvironmentPolicyChange{
			EnvironmentID: params.ID,
			Policy:        policy,
		})
	})
}

func saveEnvironmentPolicy(
	ctx context.Context,
	queries *db.Queries,
	change EnvironmentPolicyChange,
) error {
	if !change.Policy.Remote {
		return queries.DeleteEnvironmentAgentPolicy(ctx, change.EnvironmentID)
	}
	pool, err := queries.GetAgentPool(ctx, change.Policy.PoolID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEnvironmentPolicyPool
		}
		return fmt.Errorf("get agent pool: %w", err)
	}
	if pool.Enabled == 0 {
		return ErrEnvironmentPolicyPool
	}
	if err := queries.UpsertEnvironmentAgentPolicy(
		ctx,
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: change.EnvironmentID,
			PoolID:        change.Policy.PoolID,
			Selector:      change.Policy.Selector,
		},
	); err != nil {
		return fmt.Errorf("save environment policy: %w", err)
	}
	return nil
}
