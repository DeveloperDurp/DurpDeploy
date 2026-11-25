package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type RemoteAssignedDeployment struct {
	DeploymentID int64
	AgentID      string
}

func (r *Repository) CancelAssignedRemoteDeployment(
	ctx context.Context,
	assigned RemoteAssignedDeployment,
) error {
	return r.WithDeploymentTx(
		ctx,
		assigned.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			identity := RemoteLifecycleClaim{
				DeploymentID: assigned.DeploymentID,
				AgentID:      assigned.AgentID,
			}
			claim, deployment, err := lockRemoteLifecycle(
				ctx,
				q,
				identity,
				false,
			)
			if err != nil {
				return err
			}
			switch claim.State {
			case "waiting", "claimed":
				return cancelUnstartedRemoteDeployment(
					ctx, q, now, identity, deployment.Status,
				)
			case "started":
				if deployment.Status != "running" {
					return ErrRemoteLifecycleConflict
				}
				return requestRemoteCancellation(
					ctx, q, now, assigned.DeploymentID,
				)
			case "cancel_requested":
				if deployment.Status == "running" {
					return nil
				}
			case "cancelled":
				if deployment.Status == "cancelled" {
					return nil
				}
			}
			return ErrRemoteLifecycleConflict
		},
	)
}

func cancelUnstartedRemoteDeployment(
	ctx context.Context,
	q *db.Queries,
	now int64,
	identity RemoteLifecycleClaim,
	status string,
) error {
	if status != "pending" && status != "pending_approval" {
		return ErrRemoteLifecycleConflict
	}
	if err := requestRemoteCancellation(
		ctx, q, now, identity.DeploymentID,
	); err != nil {
		return err
	}
	return cancelRemoteDeploymentStatus(ctx, q, now, identity)
}

func requestRemoteCancellation(
	ctx context.Context,
	q *db.Queries,
	now, deploymentID int64,
) error {
	changed, err := q.RequestRemoteDeploymentCancellation(ctx,
		db.RequestRemoteDeploymentCancellationParams{
			Now:          now,
			DeploymentID: deploymentID,
		})
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	return nil
}

func (r *Repository) MaintainRemoteLifecycle(ctx context.Context) error {
	claims, err := r.Queries.ListRemoteLifecycleClaims(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range claims {
		if err := r.maintainRemoteClaim(ctx, candidate); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) maintainRemoteClaim(
	ctx context.Context,
	candidate db.ListRemoteLifecycleClaimsRow,
) error {
	return r.WithDeploymentTx(
		ctx,
		candidate.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			identity := RemoteLifecycleClaim{
				DeploymentID: candidate.DeploymentID,
				AgentID:      candidate.AgentID,
			}
			if candidate.State == "claimed" {
				if _, err := q.LockDeploymentLogStream(ctx, candidate.DeploymentID); err != nil {
					return err
				}
			}
			claim, deployment, err := lockRemoteLifecycle(
				ctx,
				q,
				identity,
				false,
			)
			if err != nil {
				if err == ErrRemoteLifecycleConflict {
					return nil
				}
				return err
			}
			switch claim.State {
			case "claimed":
				if claim.ClaimExpiresAt.Valid &&
					claim.ClaimExpiresAt.Int64 <= now {
					return expireUnstartedRemoteClaim(ctx, q, now, claim)
				}
			case "started":
				return loseStaleRemoteClaim(
					ctx, q, now, identity, claim, deployment,
				)
			case "cancel_requested":
				return expireStaleRemoteCancellation(
					ctx, q, now, identity, claim, deployment,
				)
			}
			return nil
		},
	)
}

func expireUnstartedRemoteClaim(
	ctx context.Context,
	q *db.Queries,
	now int64,
	claim db.RemoteDeploymentClaim,
) error {
	changed, err := q.ExpireRemoteClaim(ctx, db.ExpireRemoteClaimParams{
		Now:          now,
		DeploymentID: claim.DeploymentID,
		AgentID:      claim.AgentID,
	})
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	if claim.ExpiredClaims == 0 {
		return nil
	}
	changed, err = q.FailUnstartedRemoteDeploymentStatus(
		ctx,
		db.FailUnstartedRemoteDeploymentStatusParams{
			Now:          sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: claim.DeploymentID,
			AgentID:      sql.NullString{String: claim.AgentID, Valid: true},
		},
	)
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	_, err = q.CreateDeploymentLog(ctx, db.CreateDeploymentLogParams{
		DeploymentID: claim.DeploymentID,
		Line: fmt.Sprintf(
			"Agent %q did not start the deployment after two claims.",
			claim.AgentID,
		),
	})
	return err
}

func loseStaleRemoteClaim(
	ctx context.Context,
	q *db.Queries,
	now int64,
	identity RemoteLifecycleClaim,
	claim db.RemoteDeploymentClaim,
	deployment db.Deployment,
) error {
	staleBefore := now - int64(agentproto.LostThreshold/time.Second)
	if !claim.LastHeartbeatAt.Valid ||
		claim.LastHeartbeatAt.Int64 > staleBefore {
		return nil
	}
	changed, err := q.LoseStaleRemoteClaim(ctx, db.LoseStaleRemoteClaimParams{
		Now:          sql.NullInt64{Int64: now, Valid: true},
		DeploymentID: claim.DeploymentID,
		AgentID:      claim.AgentID,
		StaleBefore:  sql.NullInt64{Int64: staleBefore, Valid: true},
	})
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	return failRemoteDeployment(ctx, q, now, identity, deployment)
}

func expireStaleRemoteCancellation(
	ctx context.Context,
	q *db.Queries,
	now int64,
	identity RemoteLifecycleClaim,
	claim db.RemoteDeploymentClaim,
	deployment db.Deployment,
) error {
	staleBefore := now - int64(
		agentproto.CancelAcknowledgementTimeout/time.Second,
	)
	if !claim.CancelRequestedAt.Valid ||
		claim.CancelRequestedAt.Int64 > staleBefore {
		return nil
	}
	changed, err := q.ExpireRemoteCancellationClaim(ctx,
		db.ExpireRemoteCancellationClaimParams{
			Now:          sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: claim.DeploymentID,
			AgentID:      claim.AgentID,
			StaleBefore:  sql.NullInt64{Int64: staleBefore, Valid: true},
		})
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	return failRemoteDeployment(ctx, q, now, identity, deployment)
}

func failRemoteDeployment(
	ctx context.Context,
	q *db.Queries,
	now int64,
	identity RemoteLifecycleClaim,
	deployment db.Deployment,
) error {
	if deployment.Status != "running" {
		return ErrRemoteLifecycleConflict
	}
	changed, err := q.FailRemoteDeploymentStatus(ctx,
		db.FailRemoteDeploymentStatusParams{
			Now:          sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: identity.DeploymentID,
			AgentID:      sql.NullString{String: identity.AgentID, Valid: true},
		})
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	return nil
}
