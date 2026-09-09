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
		locked, err := q.LockClaimAgent(ctx, arg.AgentID)
		if err != nil || locked == 0 {
			return err
		}
		changed, err = q.ClaimRemoteDeployment(ctx, arg)
		return err
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
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

type RemoteDeploymentLog struct {
	db.LockRemoteDeploymentClaimParams
	Sequence  int64
	StepIndex sql.NullInt64
	Attempt   sql.NullInt64
	StepName  sql.NullString
	Line      string
}

func (r *Repository) AppendRemoteDeploymentLog(
	ctx context.Context,
	arg RemoteDeploymentLog,
) (db.DeploymentLog, error) {
	var log db.DeploymentLog
	err := r.WithTx(ctx, func(q *db.Queries) error {
		locked, err := q.LockRemoteDeploymentClaim(
			ctx,
			arg.LockRemoteDeploymentClaimParams,
		)
		if err != nil {
			return err
		}
		if locked == 0 {
			return sql.ErrNoRows
		}
		scope := db.GetScopedDeploymentLogParams{
			DeploymentID: arg.DeploymentID,
			StepIndex:    arg.StepIndex,
			Attempt:      arg.Attempt,
			Sequence:     arg.Sequence,
		}
		log, err = q.GetScopedDeploymentLog(ctx, scope)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		log, err = q.CreateDeploymentLog(ctx, db.CreateDeploymentLogParams{
			DeploymentID: arg.DeploymentID,
			StepName:     arg.StepName,
			Line:         arg.Line,
		})
		if err != nil {
			return err
		}
		return q.CreateDeploymentLogScope(
			ctx,
			db.CreateDeploymentLogScopeParams{
				LogID:        log.ID,
				DeploymentID: scope.DeploymentID,
				StepIndex:    scope.StepIndex,
				Attempt:      scope.Attempt,
				Sequence:     scope.Sequence,
			},
		)
	})
	if err != nil {
		return db.DeploymentLog{}, err
	}
	return log, nil
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
