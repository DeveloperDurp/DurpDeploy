package deploymentstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// Dispatch is the safe, operator-facing routing state for a deployment.
// It deliberately excludes claim tokens, hashes, and payload data.
type Dispatch struct {
	Mode            string  `json:"mode"`
	State           string  `json:"state,omitempty"`
	Reason          string  `json:"reason,omitempty"`
	Agent           *Agent  `json:"agent,omitempty"`
	Source          string  `json:"source,omitempty"`
	TargetMode      string  `json:"target_mode,omitempty"`
	AgentLabelID    *int64  `json:"agent_label_id,omitempty"`
	AgentLabelName  string  `json:"agent_label_name,omitempty"`
	AgentStrategy   string  `json:"agent_strategy,omitempty"`
	AggregateStatus string  `json:"aggregate_status,omitempty"`
	Children        []Child `json:"children,omitempty"`
}

type Agent struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	LastHeartbeatAt *int64 `json:"last_heartbeat_at,omitempty"`
}

func (dispatch Dispatch) CanCancel() bool {
	return dispatch.Mode == "local" || dispatch.State == "waiting" ||
		dispatch.State == "claimed" || dispatch.State == "started"
}

func (dispatch Dispatch) IsUncertainTerminal() bool {
	return dispatch.State == "lost" || dispatch.State == "cancel_unconfirmed"
}

func ValidFilterState(state string) bool {
	switch state {
	case "local", "waiting", "claimed", "started", "succeeded", "failed",
		"cancel_requested", "cancelled", "cancel_unconfirmed", "lost":
		return true
	default:
		return false
	}
}

func Load(
	ctx context.Context,
	repo *repository.Repository,
	deploymentID int64,
) (Dispatch, error) {
	deployment, err := repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		return Dispatch{}, fmt.Errorf("get deployment: %w", err)
	}
	result, err := loadDispatch(ctx, repo, deploymentID)
	if err != nil {
		return Dispatch{}, err
	}
	return withRouting(ctx, repo, deployment, result)
}

func loadDispatch(
	ctx context.Context,
	repo *repository.Repository,
	deploymentID int64,
) (Dispatch, error) {
	dispatch, err := repo.Queries.GetDeploymentDispatch(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Dispatch{Mode: "local"}, nil
	}
	if err != nil {
		return Dispatch{}, fmt.Errorf("get deployment dispatch: %w", err)
	}
	return fromDispatch(ctx, repo, dispatch)
}

func fromDispatch(
	ctx context.Context,
	repo *repository.Repository,
	dispatch db.DeploymentDispatch,
) (Dispatch, error) {
	result := Dispatch{Mode: dispatch.Mode, State: dispatch.State}
	if dispatch.Reason.Valid {
		result.Reason = dispatch.Reason.String
	}
	if dispatch.Mode != "remote" {
		return result, nil
	}
	if !dispatch.AgentID.Valid {
		return result, nil
	}
	agent, err := repo.Queries.GetAgent(ctx, dispatch.AgentID.String)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return Dispatch{}, fmt.Errorf("get dispatch agent: %w", err)
	}
	result.Agent = &Agent{
		ID:              agent.ID,
		Name:            agent.Name,
		Status:          agent.Status,
		LastHeartbeatAt: nullableUnix(agent.LastHeartbeatAt),
	}
	return result, nil
}

func nullableUnix(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
