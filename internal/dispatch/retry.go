package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
)

func (s *CreationService) Retry(
	ctx context.Context,
	source db.Deployment,
) (db.Deployment, error) {
	if source.ParentDeploymentID.Valid {
		return db.Deployment{}, ErrChildDeployment
	}
	release, err := s.repo.Queries.GetRelease(ctx, source.ReleaseID)
	if err != nil {
		return db.Deployment{}, fmt.Errorf("get retry release: %w", err)
	}
	project, err := s.repo.Queries.GetProject(ctx, release.ProjectID)
	if err != nil {
		return db.Deployment{}, fmt.Errorf("get retry project: %w", err)
	}
	state, err := gate.Evaluate(
		ctx, s.repo, project, release, source.EnvironmentID,
	)
	if err != nil {
		return db.Deployment{}, fmt.Errorf("check retry gate: %w", err)
	}
	if !state.Deployable {
		return db.Deployment{}, fmt.Errorf(
			"%w: %s", ErrPromotionBlocked, state.Reason,
		)
	}
	requiresApproval, err := gate.RequiresApproval(
		ctx, s.repo, project, source.EnvironmentID,
	)
	if err != nil {
		return db.Deployment{}, fmt.Errorf("check retry approval: %w", err)
	}
	policy, err := s.exactRetryPolicy(ctx, source)
	if err != nil {
		return db.Deployment{}, err
	}
	if !requiresApproval {
		if err := s.validateRetryPolicy(ctx, policy); err != nil {
			return db.Deployment{}, err
		}
	}

	var deployment db.Deployment
	var runLocal bool
	err = s.repo.WithRoutingTx(ctx, func(q *db.Queries) error {
		status := "pending"
		if requiresApproval {
			status = "pending_approval"
		}
		deployment, err = q.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID: source.ReleaseID, EnvironmentID: source.EnvironmentID,
			Status: status, Note: sql.NullString{
				String: fmt.Sprintf("Retry of #%d", source.ID), Valid: true,
			},
		})
		if err != nil {
			return fmt.Errorf("create retry deployment: %w", err)
		}
		if err := freezeIntentTx(ctx, q, deployment.ID, policy); err != nil {
			return err
		}
		if requiresApproval {
			return nil
		}
		runLocal, err = s.dispatcher.prepareTx(ctx, q, deployment, policy)
		return err
	})
	if err != nil {
		return db.Deployment{}, err
	}
	if runLocal {
		s.dispatcher.runLocal(deployment)
	}
	return deployment, nil
}

func (s *CreationService) exactRetryPolicy(
	ctx context.Context,
	source db.Deployment,
) (Policy, error) {
	snapshot, err := s.repo.Queries.GetDeploymentRoutingSnapshot(ctx, source.ID)
	if err == nil {
		policy := Policy{
			Source: SourceRetry, Mode: TargetMode(snapshot.TargetMode),
			LabelID:   snapshot.AgentLabelID.Int64,
			LabelName: snapshot.AgentLabelName.String,
			Strategy:  Strategy(snapshot.AgentStrategy.String),
		}
		rows, err := s.repo.Queries.ListDeploymentRoutingAgents(ctx, source.ID)
		if err != nil {
			return Policy{}, fmt.Errorf("list prior routing agents: %w", err)
		}
		for _, row := range rows {
			policy.ExactAgents = append(policy.ExactAgents, Agent{
				ID: row.AgentID, Name: row.AgentName,
			})
		}
		if policy.Mode == TargetLabel && len(policy.ExactAgents) == 0 {
			return Policy{}, ErrNoEligibleAgents
		}
		return policy, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf("get prior routing snapshot: %w", err)
	}
	agentID := source.TargetAgentID.String
	if agentID == "" {
		dispatchRow, dispatchErr := s.repo.Queries.GetDeploymentDispatch(
			ctx, source.ID,
		)
		if dispatchErr == nil {
			if dispatchRow.Mode == "local" {
				return Policy{Source: SourceRetry, Mode: TargetLocal}, nil
			}
			agentID = dispatchRow.AssignedAgentID.String
		} else if !errors.Is(dispatchErr, sql.ErrNoRows) {
			return Policy{}, fmt.Errorf("get prior dispatch: %w", dispatchErr)
		}
	}
	if agentID == "" {
		assignment, assignmentErr := s.repo.Queries.GetEnvironmentAgentAssignment(
			ctx,
			source.EnvironmentID,
		)
		if assignmentErr == nil {
			agentID = assignment.AgentID
		} else if !errors.Is(assignmentErr, sql.ErrNoRows) {
			return Policy{}, fmt.Errorf("get prior legacy assignment: %w", assignmentErr)
		}
	}
	if agentID != "" {
		return Policy{
			Source: SourceRetry, Mode: TargetRemote, LegacyAgentID: agentID,
		}, nil
	}
	return Policy{Source: SourceRetry, Mode: TargetLocal}, nil
}

func (s *CreationService) validateRetryPolicy(
	ctx context.Context,
	policy Policy,
) error {
	if policy.Mode == TargetRemote {
		return nil
	}
	if policy.Mode != TargetLabel {
		return nil
	}
	rows := make([]db.DeploymentRoutingAgent, len(policy.ExactAgents))
	for i, agent := range policy.ExactAgents {
		rows[i] = db.DeploymentRoutingAgent{AgentID: agent.ID}
	}
	return validateExactAgents(ctx, s.repo.Queries, rows)
}
