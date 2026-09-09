package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var ErrRemoteLifecycleConflict = errors.New("remote lifecycle conflict")

type RemoteLifecycleClaim struct {
	DeploymentID   int64
	AgentID        string
	ClaimTokenHash []byte
}

type RemoteHeartbeatResult struct {
	CancelRequested bool
}

func (r *Repository) StartRemoteDeployment(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		claim, deployment, err := lockRemoteLifecycle(ctx, q, identity, true)
		if err != nil {
			return err
		}
		switch claim.State {
		case "claimed":
			if deployment.Status != "pending" {
				return ErrRemoteLifecycleConflict
			}
			changed, err := q.StartRemoteDeployment(ctx,
				db.StartRemoteDeploymentParams{
					Now:            sql.NullInt64{Int64: now, Valid: true},
					DeploymentID:   identity.DeploymentID,
					AgentID:        identity.AgentID,
					ClaimTokenHash: identity.ClaimTokenHash,
				})
			if err != nil || changed != 1 {
				return transitionError(err)
			}
			changed, err = q.StartRemoteDeploymentStatus(ctx,
				db.StartRemoteDeploymentStatusParams{
					Now:          sql.NullInt64{Int64: now, Valid: true},
					DeploymentID: identity.DeploymentID,
					AgentID: sql.NullString{
						String: identity.AgentID,
						Valid:  true,
					},
				})
			if err != nil || changed != 1 {
				return transitionError(err)
			}
			return nil
		case "started", "cancel_requested":
			if deployment.Status == "running" && claim.StartedAt.Valid {
				return nil
			}
		}
		return ErrRemoteLifecycleConflict
	})
}

func (r *Repository) HeartbeatRemoteDeployment(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (RemoteHeartbeatResult, error) {
	result := RemoteHeartbeatResult{}
	err := r.WithTx(ctx, func(q *db.Queries) error {
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		claim, deployment, err := lockRemoteLifecycle(ctx, q, identity, true)
		if err != nil {
			return err
		}
		if deployment.Status != "running" ||
			(claim.State != "started" && claim.State != "cancel_requested") {
			return ErrRemoteLifecycleConflict
		}
		changed, err := q.HeartbeatRemoteDeployment(ctx,
			db.HeartbeatRemoteDeploymentParams{
				Now:            sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   identity.DeploymentID,
				AgentID:        identity.AgentID,
				ClaimTokenHash: identity.ClaimTokenHash,
			})
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		result.CancelRequested = claim.State == "cancel_requested"
		return nil
	})
	return result, err
}

func (r *Repository) FinishRemoteDeploymentLifecycle(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	state string,
	reason sql.NullString,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		claim, deployment, err := lockRemoteLifecycle(ctx, q, identity, true)
		if err != nil {
			return err
		}
		if claim.State == state && deployment.Status == state {
			return nil
		}
		if claim.State != "started" || deployment.Status != "running" {
			return ErrRemoteLifecycleConflict
		}
		changed, err := q.FinishRemoteDeployment(ctx,
			db.FinishRemoteDeploymentParams{
				State: state, Reason: reason,
				Now:            sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   identity.DeploymentID,
				AgentID:        identity.AgentID,
				ClaimTokenHash: identity.ClaimTokenHash,
			})
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		changed, err = q.FinishRemoteDeploymentStatus(ctx,
			db.FinishRemoteDeploymentStatusParams{
				State:        state,
				Now:          sql.NullInt64{Int64: now, Valid: true},
				DeploymentID: identity.DeploymentID,
				AgentID: sql.NullString{
					String: identity.AgentID,
					Valid:  true,
				},
			})
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		return nil
	})
}

func (r *Repository) AcknowledgeRemoteCancellation(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		claim, deployment, err := lockRemoteLifecycle(ctx, q, identity, true)
		if err != nil {
			return err
		}
		if claim.State == "cancelled" && deployment.Status == "cancelled" {
			return nil
		}
		if claim.State != "cancel_requested" || deployment.Status != "running" {
			return ErrRemoteLifecycleConflict
		}
		changed, err := q.AcknowledgeRemoteDeploymentCancellation(ctx,
			db.AcknowledgeRemoteDeploymentCancellationParams{
				Now:            sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   identity.DeploymentID,
				AgentID:        identity.AgentID,
				ClaimTokenHash: identity.ClaimTokenHash,
			})
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		return cancelRemoteDeploymentStatus(ctx, q, now, identity)
	})
}

func transitionError(err error) error {
	if err != nil {
		return err
	}
	return ErrRemoteLifecycleConflict
}

func lockRemoteLifecycle(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
	requireToken bool,
) (db.RemoteDeploymentClaim, db.Deployment, error) {
	locked, err := q.LockClaimAgent(ctx, identity.AgentID)
	if err != nil || locked != 1 {
		return db.RemoteDeploymentClaim{}, db.Deployment{}, transitionError(err)
	}
	if requireToken {
		locked, err = q.LockRemoteLifecycleClaim(ctx,
			db.LockRemoteLifecycleClaimParams{
				DeploymentID:   identity.DeploymentID,
				AgentID:        identity.AgentID,
				ClaimTokenHash: identity.ClaimTokenHash,
			})
	} else {
		locked, err = q.LockRemoteMaintenanceClaim(ctx,
			db.LockRemoteMaintenanceClaimParams{
				DeploymentID: identity.DeploymentID,
				AgentID:      identity.AgentID,
			})
	}
	if err != nil || locked != 1 {
		return db.RemoteDeploymentClaim{}, db.Deployment{}, transitionError(err)
	}
	locked, err = q.LockAssignedRemoteDeployment(ctx,
		db.LockAssignedRemoteDeploymentParams{
			DeploymentID: identity.DeploymentID,
			AgentID:      sql.NullString{String: identity.AgentID, Valid: true},
		})
	if err != nil || locked != 1 {
		return db.RemoteDeploymentClaim{}, db.Deployment{}, transitionError(err)
	}
	claim, err := q.GetRemoteDeploymentClaim(ctx, identity.DeploymentID)
	if err != nil {
		return db.RemoteDeploymentClaim{}, db.Deployment{}, err
	}
	deployment, err := q.GetDeployment(ctx, identity.DeploymentID)
	return claim, deployment, err
}

func cancelRemoteDeploymentStatus(
	ctx context.Context,
	q *db.Queries,
	now int64,
	identity RemoteLifecycleClaim,
) error {
	changed, err := q.CancelRemoteDeploymentStatus(ctx,
		db.CancelRemoteDeploymentStatusParams{
			Now:          sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: identity.DeploymentID,
			AgentID:      sql.NullString{String: identity.AgentID, Valid: true},
		})
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	return nil
}
