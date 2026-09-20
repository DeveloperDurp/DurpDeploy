package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var ErrAgentUnavailable = errors.New("agent is not active and paired")

func (r *Repository) RevokeAgent(
	ctx context.Context,
	agentID string,
) (bool, error) {
	changed := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		changed, err = revokeAgent(ctx, q, agentID)
		return err
	})
	return changed, err
}

func revokeAgent(
	ctx context.Context,
	q *db.Queries,
	agentID string,
) (bool, error) {
	locked, err := q.LockRevocableAgent(ctx, agentID)
	if err != nil {
		return false, err
	}
	agent, err := q.GetAgent(ctx, agentID)
	if err != nil {
		return false, err
	}
	if agent.Status == "revoked" {
		return false, nil
	}
	if locked != 1 {
		return false, ErrAgentUnavailable
	}
	if _, err := q.SetAgentStatus(ctx, db.SetAgentStatusParams{
		Status: "revoked",
		ID:     agentID,
	}); err != nil {
		return false, err
	}
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return false, err
	}
	if _, err := q.RevokeAgentRemoteStepRuns(
		ctx,
		db.RevokeAgentRemoteStepRunsParams{
			Now: sql.NullInt64{Int64: now, Valid: true}, AgentID: agentID,
		},
	); err != nil {
		return false, err
	}
	claims, err := q.ListRevocableAgentClaims(ctx, agentID)
	if err != nil {
		return false, err
	}
	for _, claim := range claims {
		if err := revokeAgentClaim(ctx, q, now, claim); err != nil {
			return false, err
		}
	}
	if _, err := q.FailDeploymentsWithTerminalRemoteStepRuns(
		ctx,
		sql.NullInt64{Int64: now, Valid: true},
	); err != nil {
		return false, err
	}
	return true, nil
}

func revokeAgentClaim(
	ctx context.Context,
	q *db.Queries,
	now int64,
	claim db.RemoteDeploymentClaim,
) error {
	var (
		changed int64
		err     error
	)
	if claim.StartedAt.Valid {
		changed, err = q.RevokeStartedRemoteClaim(
			ctx,
			db.RevokeStartedRemoteClaimParams{
				Now:          sql.NullInt64{Int64: now, Valid: true},
				DeploymentID: claim.DeploymentID,
				AgentID:      claim.AgentID,
			},
		)
	} else {
		changed, err = q.RevokeUnstartedRemoteClaim(
			ctx,
			db.RevokeUnstartedRemoteClaimParams{
				Now:          sql.NullInt64{Int64: now, Valid: true},
				DeploymentID: claim.DeploymentID,
				AgentID:      claim.AgentID,
			},
		)
	}
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	changed, err = q.TerminateRevokedRemoteDeployment(
		ctx,
		db.TerminateRevokedRemoteDeploymentParams{
			Now:          sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: claim.DeploymentID,
			AgentID: sql.NullString{
				String: claim.AgentID,
				Valid:  true,
			},
		},
	)
	if err != nil || changed != 1 {
		return transitionError(err)
	}
	return nil
}
