package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

// ClaimRemoteStep locks the agent before testing its capacity on all engines.
func (r *Repository) ClaimRemoteStep(
	ctx context.Context, arg db.ClaimRemoteDeploymentStepParams,
) (int64, error) {
	var changed int64
	err := r.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.LockClaimAgent(ctx, arg.AgentID.String)
		if err != nil || n == 0 {
			return err
		}
		changed, err = q.ClaimRemoteDeploymentStep(ctx, arg)
		if err != nil || changed == 0 {
			return err
		}
		_, err = q.ReplaceDeploymentDispatch(ctx,
			db.ReplaceDeploymentDispatchParams{
				DeploymentID: arg.DeploymentID, StepIndex: arg.StepIndex,
				Attempt: arg.Attempt, AgentID: arg.AgentID,
				ClaimTokenHash: arg.ClaimTokenHash,
			})
		return err
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

func (r *Repository) CommitAgentPairing(
	ctx context.Context, pairing db.CompleteAgentPairingParams,
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
		p, err := q.GetAgentPairing(ctx, pairing.AgentID)
		if err != nil {
			return err
		}
		identity.ID = p.AgentID
		identity.EncryptedIdentity = p.EncryptedIdentity
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

type RemoteStepLog struct {
	db.LockRemoteLogAttemptParams
	Sequence int64
	StepName sql.NullString
	Line     string
}

// AppendRemoteStepLog holds the attempt lock through log and scope insertion.
func (r *Repository) AppendRemoteStepLog(
	ctx context.Context, arg RemoteStepLog,
) (db.DeploymentLog, error) {
	var log db.DeploymentLog
	err := r.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.LockRemoteLogAttempt(ctx, arg.LockRemoteLogAttemptParams)
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		scope := db.GetScopedDeploymentLogParams{
			DeploymentID: arg.DeploymentID,
			StepIndex:    sql.NullInt64{Int64: arg.StepIndex, Valid: true},
			Attempt:      sql.NullInt64{Int64: arg.Attempt, Valid: true},
			Sequence:     arg.Sequence,
		}
		log, err = q.GetScopedDeploymentLog(ctx, scope)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		log, err = q.CreateDeploymentLog(ctx, db.CreateDeploymentLogParams{
			DeploymentID: arg.DeploymentID,
			StepName:     arg.StepName, Line: arg.Line,
		})
		if err != nil {
			return err
		}
		return q.CreateDeploymentLogScope(
			ctx,
			db.CreateDeploymentLogScopeParams{
				LogID: log.ID, DeploymentID: scope.DeploymentID,
				StepIndex: scope.StepIndex, Attempt: scope.Attempt,
				Sequence: scope.Sequence,
			},
		)
	})
	if err != nil {
		return db.DeploymentLog{}, err
	}
	return log, nil
}

func (r *Repository) CancelStepDeployment(
	ctx context.Context, arg db.RequestDeploymentStepCancellationParams,
) (int64, error) {
	var changed int64
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		changed, err = q.CancelStepDeployment(ctx, arg.DeploymentID)
		if err != nil || changed == 0 {
			return err
		}
		_, err = q.RequestDeploymentStepCancellation(ctx, arg)
		return err
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}
