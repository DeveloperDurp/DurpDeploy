package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

func (r *Repository) ClaimRemoteDeployment(
	ctx context.Context,
	arg db.ClaimRemoteDeploymentParams,
) (int64, error) {
	var changed int64
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		changed, err = claimRemoteDeployment(ctx, q, arg)
		return err
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

func claimRemoteDeployment(
	ctx context.Context,
	q *db.Queries,
	arg db.ClaimRemoteDeploymentParams,
) (int64, error) {
	locked, err := q.LockClaimAgent(ctx, arg.AgentID)
	if err != nil || locked == 0 {
		return 0, err
	}
	return q.ClaimRemoteDeployment(ctx, arg)
}

func (r *Repository) CommitAgentPairing(
	ctx context.Context,
	pairing db.CompleteAgentPairingParams,
	identity db.ActivatePairedAgentParams,
) (int64, error) {
	err := r.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.CompleteAgentPairing(ctx, pairing)
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		persisted, err := q.GetAgentPairing(ctx, pairing.AgentID)
		if err != nil {
			return err
		}
		identity.ID = persisted.AgentID
		identity.EncryptedIdentity = persisted.EncryptedIdentity
		n, err = q.ActivatePairedAgent(ctx, identity)
		if err == nil && n == 0 {
			return sql.ErrNoRows
		}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return 1, nil
}

func (r *Repository) CancelRemoteDeployment(
	ctx context.Context,
	arg db.RequestRemoteDeploymentCancellationParams,
) (int64, error) {
	var changed int64
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		changed, err = q.CancelStepDeployment(ctx, arg.DeploymentID)
		if err != nil || changed == 0 {
			return err
		}
		_, err = q.RequestRemoteDeploymentCancellation(ctx, arg)
		return err
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}
