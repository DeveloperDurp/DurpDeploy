package deploymentstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const ParentLogMessage = "Fan-out parents have no logs. Open a child deployment to view its logs."

type Child struct {
	ID        int64    `json:"id"`
	Status    string   `json:"status"`
	Dispatch  Dispatch `json:"dispatch"`
	DetailURL string   `json:"detail_url"`
	LogsURL   string   `json:"logs_url"`
	SSEURL    string   `json:"sse_url"`
	NDJSONURL string   `json:"ndjson_url"`
	TextURL   string   `json:"text_url"`
}

func IsParent(
	ctx context.Context,
	repo *repository.Repository,
	id int64,
) (bool, error) {
	snapshot, err := routingSnapshot(ctx, repo, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get routing snapshot: %w", err)
	}
	return snapshot.AgentStrategy.String == "all", nil
}

func withRouting(
	ctx context.Context,
	repo *repository.Repository,
	deployment db.Deployment,
	result Dispatch,
) (Dispatch, error) {
	rootID := deployment.ID
	if deployment.ParentDeploymentID.Valid {
		rootID = deployment.ParentDeploymentID.Int64
	}
	snapshot, err := routingSnapshot(ctx, repo, rootID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Dispatch{}, fmt.Errorf("get routing snapshot: %w", err)
	}
	if err == nil {
		result.Source = snapshot.Source
		result.TargetMode = snapshot.TargetMode
		result.AgentLabelID = nullableUnix(snapshot.AgentLabelID)
		result.AgentLabelName = snapshot.AgentLabelName.String
		result.AgentStrategy = snapshot.AgentStrategy.String
	} else {
		result.Source = "legacy"
		result.TargetMode = result.Mode
	}
	if deployment.TargetAgentID.Valid {
		result.Mode = "remote"
		result.Agent = &Agent{
			ID:     deployment.TargetAgentID.String,
			Name:   deployment.TargetAgentName.String,
			Status: "deleted",
		}
		agent, err := repo.Queries.GetAgent(
			ctx,
			deployment.TargetAgentID.String,
		)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Dispatch{}, fmt.Errorf("get routing agent: %w", err)
		}
		if err == nil {
			result.Agent.Status = agent.Status
			result.Agent.LastHeartbeatAt = nullableUnix(agent.LastHeartbeatAt)
		}
	}
	if !deployment.ParentDeploymentID.Valid && result.AgentStrategy == "all" {
		result.Mode = "fanout"
		result.AggregateStatus = deployment.Status
		children, err := repo.Queries.ListDeploymentChildren(
			ctx,
			sql.NullInt64{Int64: deployment.ID, Valid: true},
		)
		if err != nil {
			return Dispatch{}, fmt.Errorf("list child deployments: %w", err)
		}
		result.Children = make([]Child, 0, len(children))
		for _, child := range children {
			routing, err := Load(ctx, repo, child.ID)
			if err != nil {
				return Dispatch{}, err
			}
			base := fmt.Sprintf("/api/v1/deployments/%d", child.ID)
			result.Children = append(result.Children, Child{
				ID: child.ID, Status: child.Status, Dispatch: routing,
				DetailURL: fmt.Sprintf(
					"/deployments/%d",
					child.ID,
				), LogsURL: base + "/logs",
				SSEURL: base + "/logs/stream", NDJSONURL: base + "/logs/stream?format=ndjson", TextURL: base + "/logs.txt",
			})
		}
	}
	return result, nil
}

func routingSnapshot(
	ctx context.Context,
	repo *repository.Repository,
	id int64,
) (db.DeploymentRoutingSnapshot, error) {
	snapshot, err := repo.Queries.GetDeploymentRoutingSnapshot(ctx, id)
	if !errors.Is(err, sql.ErrNoRows) {
		return snapshot, err
	}
	occurrence, err := repo.Queries.GetScheduledDeploymentOccurrenceByDeployment(
		ctx,
		id,
	)
	if err != nil {
		return db.DeploymentRoutingSnapshot{}, err
	}
	return db.DeploymentRoutingSnapshot{
		DeploymentID:   id,
		Source:         occurrence.RoutingSource,
		TargetMode:     occurrence.TargetMode,
		AgentLabelID:   occurrence.AgentLabelID,
		AgentLabelName: occurrence.AgentLabelName,
		AgentStrategy:  occurrence.AgentStrategy,
	}, nil
}
