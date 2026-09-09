package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var ErrRemoteLifecycleConflict = errors.New("remote lifecycle conflict")
var ErrInvalidRemoteLog = errors.New("invalid remote log")

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
		return startRemoteDeployment(ctx, q, identity)
	})
}

func startRemoteDeployment(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
) error {
	claim, deployment, err := lockRemoteLifecycle(ctx, q, identity, true)
	if err != nil {
		return err
	}
	now, err := q.CurrentUnixTime(ctx)
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
}

func (r *Repository) HeartbeatRemoteDeployment(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (RemoteHeartbeatResult, error) {
	result := RemoteHeartbeatResult{}
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		result, err = heartbeatRemoteDeployment(ctx, q, identity)
		return err
	})
	return result, err
}

func heartbeatRemoteDeployment(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
) (RemoteHeartbeatResult, error) {
	claim, deployment, err := lockRemoteLifecycle(ctx, q, identity, true)
	if err != nil {
		return RemoteHeartbeatResult{}, err
	}
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return RemoteHeartbeatResult{}, err
	}
	if deployment.Status != "running" ||
		(claim.State != "started" && claim.State != "cancel_requested") {
		return RemoteHeartbeatResult{}, ErrRemoteLifecycleConflict
	}
	changed, err := q.HeartbeatRemoteDeployment(ctx,
		db.HeartbeatRemoteDeploymentParams{
			Now:            sql.NullInt64{Int64: now, Valid: true},
			DeploymentID:   identity.DeploymentID,
			AgentID:        identity.AgentID,
			ClaimTokenHash: identity.ClaimTokenHash,
		})
	if err != nil || changed != 1 {
		return RemoteHeartbeatResult{}, transitionError(err)
	}
	return RemoteHeartbeatResult{
		CancelRequested: claim.State == "cancel_requested",
	}, nil
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
