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
	Mode   string `json:"mode"`
	State  string `json:"state,omitempty"`
	Reason string `json:"reason,omitempty"`
	Agent  *Agent `json:"agent,omitempty"`
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
	if dispatch.Mode != "remote" {
		return result, nil
	}
	if dispatch.State == "waiting" && dispatch.Reason.Valid {
		result.Reason = dispatch.Reason.String
	}
	if !dispatch.AgentID.Valid {
		return result, nil
	}
	agent, err := repo.Queries.GetAgent(ctx, dispatch.AgentID.String)
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
