package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

func freezeIntentTx(
	ctx context.Context,
	queries *db.Queries,
	deploymentID int64,
	policy Policy,
) error {
	if policy.Mode == TargetRemote {
		agent, err := queries.GetAgent(ctx, policy.LegacyAgentID)
		if err != nil {
			return fmt.Errorf("get legacy target agent: %w", err)
		}
		if err := queries.SetDeploymentTargetAgent(
			ctx,
			db.SetDeploymentTargetAgentParams{
				TargetAgentID: sql.NullString{String: agent.ID, Valid: true},
				TargetAgentName: sql.NullString{
					String: agent.Name,
					Valid:  true,
				},
				ID: deploymentID,
			},
		); err != nil {
			return fmt.Errorf("freeze legacy target agent: %w", err)
		}
		return nil
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	if err := createSnapshot(ctx, queries, deploymentID, policy); err != nil {
		return err
	}
	for position, agent := range policy.ExactAgents {
		if _, err := queries.AddDeploymentRoutingAgent(
			ctx, db.AddDeploymentRoutingAgentParams{
				DeploymentID: deploymentID, Position: int64(position),
				AgentID: agent.ID, AgentName: agent.Name,
			},
		); err != nil {
			return fmt.Errorf("freeze exact routing agent: %w", err)
		}
	}
	return nil
}

func policyForApproval(
	ctx context.Context,
	queries *db.Queries,
	deployment db.Deployment,
) (Policy, error) {
	snapshot, err := queries.GetDeploymentRoutingSnapshot(ctx, deployment.ID)
	if err == nil {
		policy := Policy{
			Source: Source(
				snapshot.Source,
			), Mode: TargetMode(snapshot.TargetMode),
			LabelID:   snapshot.AgentLabelID.Int64,
			LabelName: snapshot.AgentLabelName.String,
			Strategy:  Strategy(snapshot.AgentStrategy.String),
		}
		if policy.Source == SourceRetry {
			rows, listErr := queries.ListDeploymentRoutingAgents(
				ctx, deployment.ID,
			)
			if listErr != nil {
				return Policy{}, fmt.Errorf(
					"list exact routing agents: %w",
					listErr,
				)
			}
			for _, row := range rows {
				policy.ExactAgents = append(policy.ExactAgents, Agent{
					ID: row.AgentID, Name: row.AgentName,
				})
			}
		}
		return policy, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf("get routing snapshot: %w", err)
	}
	occurrence, err := queries.GetScheduledDeploymentOccurrenceByDeployment(
		ctx, deployment.ID,
	)
	if err == nil {
		return scheduledOccurrencePolicy(occurrence), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf("get scheduled occurrence: %w", err)
	}
	if deployment.TargetAgentID.Valid {
		return Policy{
			Source:        SourceLegacy,
			Mode:          TargetRemote,
			LegacyAgentID: deployment.TargetAgentID.String,
		}, nil
	}
	assignment, err := queries.GetEnvironmentAgentAssignment(
		ctx, deployment.EnvironmentID,
	)
	if err == nil {
		return Policy{
			Source: SourceLegacy, Mode: TargetRemote,
			LegacyAgentID: assignment.AgentID,
		}, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{Source: SourceLegacy, Mode: TargetLocal}, nil
	}
	return Policy{}, fmt.Errorf("get legacy environment assignment: %w", err)
}

func (d *Dispatcher) prepareTx(
	ctx context.Context,
	queries *db.Queries,
	deployment db.Deployment,
	policy Policy,
) (bool, error) {
	if _, err := queries.GetDeploymentDispatch(ctx, deployment.ID); err == nil {
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("get deployment dispatch: %w", err)
	}
	var agents []Agent
	if policy.Mode != TargetRemote {
		if _, err := queries.GetDeploymentRoutingSnapshot(
			ctx, deployment.ID,
		); errors.Is(err, sql.ErrNoRows) {
			if err := createSnapshot(
				ctx, queries, deployment.ID, policy,
			); err != nil {
				return false, err
			}
		} else if err != nil {
			return false, fmt.Errorf("get routing snapshot: %w", err)
		}
	}
	if policy.Mode == TargetLabel {
		rows, err := queries.ListDeploymentRoutingAgents(ctx, deployment.ID)
		if err != nil {
			return false, fmt.Errorf("list routing agents: %w", err)
		}
		if len(rows) == 0 {
			if err := selectAgents(ctx, queries, deployment.ID, policy); err != nil {
				return false, err
			}
			rows, err = queries.ListDeploymentRoutingAgents(ctx, deployment.ID)
			if err != nil {
				return false, fmt.Errorf("list routing agents: %w", err)
			}
		} else if policy.Source == SourceRetry {
			if err := validateExactAgents(ctx, queries, rows); err != nil {
				return false, err
			}
		}
		agents = make([]Agent, len(rows))
		for i, row := range rows {
			agents[i] = Agent{ID: row.AgentID, Name: row.AgentName}
		}
		if policy.Strategy == StrategyAll {
			if d.box == nil {
				return false, errors.New(
					"remote dispatch requires a secret box",
				)
			}
			return false, d.expandFanoutTx(ctx, queries, deployment.ID, agents)
		}
	}

	agentID := policy.LegacyAgentID
	if len(agents) == 1 {
		agentID = agents[0].ID
	}
	if agentID != "" {
		return d.prepareRemoteTx(ctx, queries, deployment, agentID)
	}
	_, err := queries.CreateDeploymentDispatch(
		ctx,
		db.CreateDeploymentDispatchParams{
			DeploymentID: deployment.ID, Mode: "local", State: "waiting",
		},
	)
	if err != nil {
		return false, fmt.Errorf("create local dispatch: %w", err)
	}
	return true, nil
}

func validateExactAgents(
	ctx context.Context,
	queries *db.Queries,
	agents []db.DeploymentRoutingAgent,
) error {
	for _, selected := range agents {
		agent, err := queries.GetAgent(ctx, selected.AgentID)
		if err != nil || agent.Status != "active" {
			return ErrNoEligibleAgents
		}
		pairing, err := queries.GetAgentPairing(ctx, selected.AgentID)
		if err != nil || pairing.State != "paired" {
			return ErrNoEligibleAgents
		}
	}
	return nil
}

func (d *Dispatcher) prepareRemoteTx(
	ctx context.Context,
	queries *db.Queries,
	deployment db.Deployment,
	agentID string,
) (bool, error) {
	if d.box == nil {
		return false, errors.New("remote dispatch requires a secret box")
	}
	ciphertext, err := d.buildPayload(ctx, queries, deployment)
	if err != nil {
		return false, err
	}
	if _, err := queries.CreateDeploymentPayload(
		ctx,
		db.CreateDeploymentPayloadParams{
			DeploymentID: deployment.ID, Ciphertext: ciphertext,
		},
	); err != nil {
		return false, fmt.Errorf("create deployment payload: %w", err)
	}
	if _, err := queries.CreateDirectDeploymentDispatch(
		ctx,
		db.CreateDirectDeploymentDispatchParams{
			DeploymentID:    deployment.ID,
			AssignedAgentID: sql.NullString{String: agentID, Valid: true},
		},
	); err != nil {
		return false, fmt.Errorf("create remote dispatch: %w", err)
	}
	return false, nil
}

func (d *Dispatcher) runLocal(deployment db.Deployment) {
	if d.runner == nil {
		return
	}
	go d.runner.Run(
		context.Background(), deployment.ID, deployment.ReleaseID,
		deployment.EnvironmentID,
	)
}
