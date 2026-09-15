package repository

import (
	"context"
	"database/sql"
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
	return r.WithTx(ctx, func(q *db.Queries) error {
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
			if deployment.Status != "pending" &&
				deployment.Status != "pending_approval" {
				return ErrRemoteLifecycleConflict
			}
			changed, err := q.RequestRemoteDeploymentCancellation(ctx,
				db.RequestRemoteDeploymentCancellationParams{
					Now:          now,
					DeploymentID: assigned.DeploymentID,
				})
			if err != nil || changed != 1 {
				return transitionError(err)
			}
			return cancelRemoteDeploymentStatus(ctx, q, now, identity)
		case "started":
			if deployment.Status != "running" {
				return ErrRemoteLifecycleConflict
			}
			changed, err := q.RequestRemoteDeploymentCancellation(ctx,
				db.RequestRemoteDeploymentCancellationParams{
					Now:          now,
					DeploymentID: assigned.DeploymentID,
				})
			if err != nil || changed != 1 {
				return transitionError(err)
			}
			return nil
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
	})
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
	candidate db.RemoteDeploymentClaim,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		identity := RemoteLifecycleClaim{
			DeploymentID: candidate.DeploymentID,
			AgentID:      candidate.AgentID,
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
			if claim.ClaimExpiresAt.Valid && claim.ClaimExpiresAt.Int64 <= now {
				changed, err := q.ExpireRemoteClaim(ctx,
					db.ExpireRemoteClaimParams{
						Now:          now,
						DeploymentID: claim.DeploymentID,
						AgentID:      claim.AgentID,
					})
				if err != nil || changed != 1 {
					return transitionError(err)
				}
			}
		case "started":
			staleBefore := now - int64(agentproto.LostThreshold/time.Second)
			if claim.LastHeartbeatAt.Valid &&
				claim.LastHeartbeatAt.Int64 <= staleBefore {
				changed, err := q.LoseStaleRemoteClaim(ctx,
					db.LoseStaleRemoteClaimParams{
						Now:          sql.NullInt64{Int64: now, Valid: true},
						DeploymentID: claim.DeploymentID,
						AgentID:      claim.AgentID,
						StaleBefore: sql.NullInt64{
							Int64: staleBefore,
							Valid: true,
						},
					})
				if err != nil || changed != 1 {
					return transitionError(err)
				}
				return failRemoteDeployment(ctx, q, now, identity, deployment)
			}
		case "cancel_requested":
			staleBefore := now - int64(
				agentproto.CancelAcknowledgementTimeout/time.Second,
			)
			if claim.CancelRequestedAt.Valid &&
				claim.CancelRequestedAt.Int64 <= staleBefore {
				changed, err := q.ExpireRemoteCancellationClaim(ctx,
					db.ExpireRemoteCancellationClaimParams{
						Now:          sql.NullInt64{Int64: now, Valid: true},
						DeploymentID: claim.DeploymentID,
						AgentID:      claim.AgentID,
						StaleBefore: sql.NullInt64{
							Int64: staleBefore,
							Valid: true,
						},
					})
				if err != nil || changed != 1 {
					return transitionError(err)
				}
				return failRemoteDeployment(ctx, q, now, identity, deployment)
			}
		}
		return nil
	})
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
