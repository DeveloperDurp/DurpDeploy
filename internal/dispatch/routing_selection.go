package dispatch

import (
	"context"
	"database/sql"
	"durpdeploy/internal/db"
	"errors"
	"fmt"
)

func (r *Resolver) Freeze(
	ctx context.Context,
	deploymentID int64,
	policy Policy,
) error {
	if err := validatePolicy(policy); err != nil {
		return err
	}
	return r.repo.WithRoutingTx(ctx, func(queries *db.Queries) error {
		_, err := queries.GetDeploymentRoutingSnapshot(ctx, deploymentID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get routing snapshot: %w", err)
		}
		if err := createSnapshot(ctx, queries, deploymentID, policy); err != nil {
			return err
		}
		if policy.Mode == TargetLocal {
			return nil
		}
		return selectAgents(ctx, queries, deploymentID, policy)
	})
}

func (r *Resolver) SelectedAgents(
	ctx context.Context,
	deploymentID int64,
) ([]Agent, error) {
	rows, err := r.repo.Queries.ListDeploymentRoutingAgents(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list deployment routing agents: %w", err)
	}
	agents := make([]Agent, len(rows))
	for i, row := range rows {
		agents[i] = Agent{ID: row.AgentID, Name: row.AgentName}
	}
	return agents, nil
}

func (r *Resolver) SelectForDispatch(
	ctx context.Context,
	deploymentID int64,
) ([]Agent, Strategy, bool, error) {
	var selected []Agent
	var strategy Strategy
	hasSnapshot := false
	err := r.repo.WithRoutingTx(ctx, func(queries *db.Queries) error {
		snapshot, err := queries.GetDeploymentRoutingSnapshot(ctx, deploymentID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get routing snapshot: %w", err)
		}
		hasSnapshot = true
		strategy = Strategy(snapshot.AgentStrategy.String)
		deployment, err := queries.GetDeployment(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		if deployment.Status == "pending_approval" {
			return nil
		}
		rows, err := queries.ListDeploymentRoutingAgents(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("list deployment routing agents: %w", err)
		}
		if len(rows) == 0 && snapshot.TargetMode == string(TargetLabel) {
			policy := Policy{
				Source: Source(snapshot.Source), Mode: TargetLabel,
				LabelID:   snapshot.AgentLabelID.Int64,
				LabelName: snapshot.AgentLabelName.String,
				Strategy:  Strategy(snapshot.AgentStrategy.String),
			}
			if err := validatePolicy(policy); err != nil {
				return err
			}
			if err := selectAgents(ctx, queries, deploymentID, policy); err != nil {
				return err
			}
			rows, err = queries.ListDeploymentRoutingAgents(ctx, deploymentID)
			if err != nil {
				return fmt.Errorf("list selected routing agents: %w", err)
			}
		}
		selected = make([]Agent, len(rows))
		for i, row := range rows {
			selected[i] = Agent{ID: row.AgentID, Name: row.AgentName}
		}
		return nil
	})
	if err != nil {
		return nil, "", false, err
	}
	return selected, strategy, hasSnapshot, nil
}

func validatePolicy(policy Policy) error {
	switch policy.Source {
	case SourceLegacy, SourceProject, SourceRequest, SourceSchedule,
		SourceRetry, SourceRedeploy:
	default:
		return fmt.Errorf("%w: source %q", ErrInvalidPolicy, policy.Source)
	}
	if policy.Mode == TargetLocal && policy.LabelID == 0 &&
		policy.LabelName == "" && policy.Strategy == "" {
		return nil
	}
	if policy.Mode == TargetLabel && policy.LabelID > 0 &&
		policy.LabelName != "" &&
		(policy.Strategy == StrategyRoundRobin || policy.Strategy == StrategyAll) {
		return nil
	}
	return fmt.Errorf("%w: inconsistent resolved policy", ErrInvalidPolicy)
}

func createSnapshot(
	ctx context.Context,
	queries *db.Queries,
	deploymentID int64,
	policy Policy,
) error {
	params := db.CreateDeploymentRoutingSnapshotParams{
		DeploymentID: deploymentID,
		Source:       string(policy.Source),
		TargetMode:   string(policy.Mode),
	}
	if policy.Mode == TargetLabel {
		params.AgentLabelID = sql.NullInt64{Int64: policy.LabelID, Valid: true}
		params.AgentLabelName = sql.NullString{
			String: policy.LabelName,
			Valid:  true,
		}
		params.AgentStrategy = sql.NullString{
			String: string(policy.Strategy),
			Valid:  true,
		}
	}
	if _, err := queries.CreateDeploymentRoutingSnapshot(ctx, params); err != nil {
		return fmt.Errorf("create routing snapshot: %w", err)
	}
	return nil
}

func selectAgents(
	ctx context.Context,
	queries *db.Queries,
	deploymentID int64,
	policy Policy,
) error {
	members, err := queries.ListEligibleAgentLabelMembers(ctx, policy.LabelID)
	if err != nil {
		return fmt.Errorf("list eligible label members: %w", err)
	}
	if len(members) == 0 {
		return ErrNoEligibleAgents
	}
	selected := members
	if policy.Strategy == StrategyRoundRobin {
		if err := queries.EnsureAgentLabelCursor(ctx, policy.LabelID); err != nil {
			return fmt.Errorf("ensure label cursor: %w", err)
		}
		cursor, err := queries.LockAgentLabelCursor(ctx, policy.LabelID)
		if err != nil {
			return fmt.Errorf("lock label cursor: %w", err)
		}
		index := 0
		if cursor.LastAgentID.Valid {
			for i, member := range members {
				if member.ID > cursor.LastAgentID.String {
					index = i
					break
				}
			}
		}
		selected = members[index : index+1]
		_, err = queries.AdvanceAgentLabelCursor(
			ctx,
			db.AdvanceAgentLabelCursorParams{
				AgentLabelID: policy.LabelID,
				LastAgentID: sql.NullString{
					String: selected[0].ID, Valid: true,
				},
			},
		)
		if err != nil {
			return fmt.Errorf("advance label cursor: %w", err)
		}
	}
	for position, member := range selected {
		_, err := queries.AddDeploymentRoutingAgent(
			ctx,
			db.AddDeploymentRoutingAgentParams{
				DeploymentID: deploymentID, Position: int64(position),
				AgentID: member.ID, AgentName: member.Name,
			},
		)
		if err != nil {
			return fmt.Errorf("add routing agent: %w", err)
		}
	}
	return nil
}
