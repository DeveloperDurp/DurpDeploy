package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

const remoteAgentFailedReason = "remote_agent_failed"

type RemoteTerminalResult struct {
	Changed         bool
	DeploymentID    int64
	ProjectID       int64
	EnvironmentID   int64
	EnvironmentName string
	State           string
}

func (r *Repository) FinishRemoteDeploymentLifecycle(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	state string,
) (RemoteTerminalResult, error) {
	result := RemoteTerminalResult{}
	err := r.WithTx(ctx, func(q *db.Queries) error {
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
		if (state != "succeeded" && state != "failed") ||
			claim.State != "started" || deployment.Status != "running" {
			return ErrRemoteLifecycleConflict
		}
		release, err := q.GetRelease(ctx, deployment.ReleaseID)
		if err != nil {
			return err
		}
		environment, err := q.GetEnvironment(ctx, deployment.EnvironmentID)
		if err != nil {
			return err
		}
		reason := sql.NullString{}
		if state == "failed" {
			reason = sql.NullString{
				String: remoteAgentFailedReason,
				Valid:  true,
			}
		}
		changed, err := q.FinishRemoteDeployment(
			ctx,
			db.FinishRemoteDeploymentParams{
				State: state, Reason: reason,
				Now:            sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   identity.DeploymentID,
				AgentID:        identity.AgentID,
				ClaimTokenHash: identity.ClaimTokenHash,
			},
		)
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		changed, err = q.FinishRemoteDeploymentStatus(
			ctx,
			db.FinishRemoteDeploymentStatusParams{
				State:        state,
				Now:          sql.NullInt64{Int64: now, Valid: true},
				DeploymentID: identity.DeploymentID,
				AgentID: sql.NullString{
					String: identity.AgentID,
					Valid:  true,
				},
			},
		)
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		result = RemoteTerminalResult{
			Changed:         true,
			DeploymentID:    deployment.ID,
			ProjectID:       release.ProjectID,
			EnvironmentID:   environment.ID,
			EnvironmentName: environment.Name,
			State:           state,
		}
		return nil
	})
	return result, err
}

func (r *Repository) AcknowledgeRemoteCancellation(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (bool, error) {
	changed := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
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
		rows, err := q.AcknowledgeRemoteDeploymentCancellation(
			ctx,
			db.AcknowledgeRemoteDeploymentCancellationParams{
				Now:            sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   identity.DeploymentID,
				AgentID:        identity.AgentID,
				ClaimTokenHash: identity.ClaimTokenHash,
			},
		)
		if err != nil || rows != 1 {
			return transitionError(err)
		}
		if err := cancelRemoteDeploymentStatus(
			ctx, q, now, identity,
		); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

func cancelRemoteDeploymentStatus(
	ctx context.Context,
	q *db.Queries,
	now int64,
	identity RemoteLifecycleClaim,
) error {
	changed, err := q.CancelRemoteDeploymentStatus(
		ctx,
		db.CancelRemoteDeploymentStatusParams{
			Now:          sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: identity.DeploymentID,
			AgentID: sql.NullString{
				String: identity.AgentID,
				Valid:  true,
			},
		},
	)
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	return nil
}
