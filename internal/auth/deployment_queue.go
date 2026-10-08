package auth

import (
	"context"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type DeploymentQueueInfo struct {
	Status             string `json:"-"`
	QueuePosition      int64  `json:"queue_position"`
	ActiveDeploymentID int64  `json:"active_deployment_id,omitempty"`
	ActiveWorkURL      string `json:"active_work_url,omitempty"`
}

// ReadDeploymentQueue does not expose another project's active work.
func ReadDeploymentQueue(
	ctx context.Context, repo *repository.Repository, deploymentID int64,
) (DeploymentQueueInfo, error) {
	user := UserFromContext(ctx)
	var userID, isAdmin int64
	if user != nil {
		userID = user.ID
		if user.Role == "admin" {
			isAdmin = 1
		}
	}
	state, err := repo.Queries.GetDeploymentQueueState(ctx,
		db.GetDeploymentQueueStateParams{
			DeploymentID: deploymentID, UserID: userID, IsAdmin: isAdmin,
		})
	if err != nil {
		return DeploymentQueueInfo{}, err
	}
	info := DeploymentQueueInfo{Status: state.Status,
		QueuePosition: state.QueuePosition}
	if !state.ActiveDeploymentID.Valid {
		return info, nil
	}
	info.ActiveDeploymentID = state.ActiveDeploymentID.Int64
	info.ActiveWorkURL = fmt.Sprintf("/deployments/%d", info.ActiveDeploymentID)
	if state.Kind.String == "runbook" && state.RunbookExecutionID.Valid {
		info.ActiveWorkURL = fmt.Sprintf("/projects/%d/runbooks/executions/%d",
			state.ProjectID.Int64, state.RunbookExecutionID.Int64)
	}
	return info, nil
}
