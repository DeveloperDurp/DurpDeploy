package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var (
	ErrAgentAssignmentConflict = errors.New(
		"environment is assigned to another agent",
	)
	ErrAgentAssignmentNotFound = errors.New("agent assignment not found")
	ErrAgentUnavailable        = errors.New("agent is not active and paired")
)

func (r *Repository) AssignEnvironmentAgent(
	ctx context.Context,
	environmentID int64,
	agentID string,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		return assignEnvironmentAgent(ctx, q, environmentID, agentID)
	})
}

func assignEnvironmentAgent(
	ctx context.Context,
	q *db.Queries,
	environmentID int64,
	agentID string,
) error {
	locked, err := q.LockClaimAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if locked != 1 {
		return ErrAgentUnavailable
	}
	agent, err := q.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	pairing, err := q.GetAgentPairing(ctx, agentID)
	if err != nil || agent.Status != "active" || pairing.State != "paired" {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return ErrAgentUnavailable
	}
	if _, err := q.GetEnvironment(ctx, environmentID); err != nil {
		return err
	}
	existing, err := q.GetEnvironmentAgentAssignment(ctx, environmentID)
	if err == nil {
		if existing.AgentID == agentID {
			return nil
		}
		return ErrAgentAssignmentConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	changed, err := q.AssignEnvironmentAgent(
		ctx,
		db.AssignEnvironmentAgentParams{
			EnvironmentID: environmentID,
			AgentID:       agentID,
		},
	)
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrAgentAssignmentConflict
	}
	return nil
}

func (r *Repository) UnassignEnvironmentAgent(
	ctx context.Context,
	environmentID int64,
	agentID string,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		locked, err := q.LockClaimAgent(ctx, agentID)
		if err != nil {
			return err
		}
		if locked != 1 {
			return ErrAgentUnavailable
		}
		changed, err := q.UnassignEnvironmentAgent(
			ctx,
			db.UnassignEnvironmentAgentParams{
				EnvironmentID: environmentID,
				AgentID:       agentID,
			},
		)
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrAgentAssignmentNotFound
		}
		return nil
	})
}

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
	locked, err := q.LockClaimAgent(ctx, agentID)
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
	assignments, err := q.ListAgentAssignments(ctx, agentID)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		changed, err := q.UnassignEnvironmentAgent(
			ctx,
			db.UnassignEnvironmentAgentParams{
				EnvironmentID: assignment.EnvironmentID,
				AgentID:       agentID,
			},
		)
		if err != nil || changed != 1 {
			return false, transitionError(err)
		}
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
