package repository

import (
	"context"
	"database/sql"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
)

func (r *Repository) StartRemoteDeployment(
	ctx context.Context, identity RemoteLifecycleClaim,
) error {
	_, err := r.StartRemoteDeploymentWithLogs(ctx, identity, nil)
	return err
}

func (r *Repository) StartRemoteDeploymentWithLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, error) {
	var inserted []db.DeploymentLog
	err := r.WithDeploymentTx(
		ctx,
		identity.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			inserted = nil
			d, err := q.GetDeployment(ctx, identity.DeploymentID)
			if err != nil {
				return err
			}
			if err := advanceEnvironmentQueue(
				ctx,
				q,
				d.EnvironmentID,
			); err != nil {
				return err
			}
			owner, err := q.GetDeploymentSlot(ctx, d.ID)
			if err != nil || owner != d.ID {
				return ErrRemoteLifecycleConflict
			}
			blockers, err := q.ListDeploymentQueueBlockers(
				ctx,
				d.ID,
			)
			if err != nil {
				return err
			}
			for _, blocker := range blockers {
				if blocker != d.ID {
					return ErrRemoteLifecycleConflict
				}
			}
			claim, err := q.GetRemoteDeploymentClaim(ctx, identity.DeploymentID)
			if err != nil {
				return err
			}
			if err := startRemoteDeployment(ctx, q, identity); err != nil {
				return err
			}
			if scrubber == nil || claim.State != "claimed" {
				return nil
			}
			agent, err := q.GetAgent(ctx, identity.AgentID)
			if err != nil {
				return err
			}
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			log, err := q.CreateRemoteDeploymentLog(ctx,
				db.CreateRemoteDeploymentLogParams{
					DeploymentID: identity.DeploymentID, CreatedAt: now,
					Line: scrubber.Scrub(fmt.Sprintf(
						"Execution agent: agent=%q id=%q",
						scrubber.Scrub(agent.Name), scrubber.Scrub(agent.ID),
					)),
				},
			)
			if err != nil {
				return err
			}
			inserted = append(inserted, log)
			return nil
		},
	)
	return inserted, err
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
