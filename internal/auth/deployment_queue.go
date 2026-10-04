package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type DeploymentQueueInfo struct {
	QueuePosition      int64  `json:"queue_position"`
	ActiveDeploymentID int64  `json:"active_deployment_id,omitempty"`
	ActiveWorkURL      string `json:"active_work_url,omitempty"`
}

// ReadDeploymentQueue does not expose another project's active work.
func ReadDeploymentQueue(
	ctx context.Context, repo *repository.Repository, deploymentID int64,
) (DeploymentQueueInfo, error) {
	d, err := repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		return DeploymentQueueInfo{}, err
	}
	position, err := repo.Queries.GetDeploymentQueuePosition(ctx, deploymentID)
	if err != nil {
		return DeploymentQueueInfo{}, err
	}
	info := DeploymentQueueInfo{QueuePosition: position}
	user := UserFromContext(ctx)
	if user == nil {
		return info, nil
	}
	var isAdmin int64
	if user.Role == "admin" {
		isAdmin = 1
	}
	active, err := repo.Queries.GetVisibleEnvironmentDeploymentSlot(ctx,
		db.GetVisibleEnvironmentDeploymentSlotParams{
			EnvironmentID: d.EnvironmentID, UserID: user.ID, IsAdmin: isAdmin,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil
	}
	if err != nil {
		return DeploymentQueueInfo{}, err
	}
	info.ActiveDeploymentID = active.ID
	info.ActiveWorkURL = fmt.Sprintf("/deployments/%d", active.ID)
	if active.Kind == "runbook" && active.RunbookExecutionID.Valid {
		info.ActiveWorkURL = fmt.Sprintf("/projects/%d/runbooks/executions/%d",
			active.ProjectID, active.RunbookExecutionID.Int64)
	}
	return info, nil
}
