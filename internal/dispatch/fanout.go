package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

func (d *Dispatcher) expandFanout(
	ctx context.Context,
	deploymentID int64,
	agents []Agent,
) error {
	if d.box == nil {
		return errors.New("remote dispatch requires a secret box")
	}
	return d.repo.WithRoutingTx(ctx, func(q *db.Queries) error {
		return d.expandFanoutTx(ctx, q, deploymentID, agents)
	})
}

func (d *Dispatcher) expandFanoutTx(
	ctx context.Context,
	q *db.Queries,
	deploymentID int64,
	agents []Agent,
) error {
	parent, err := q.GetDeployment(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get parent deployment: %w", err)
	}
	if parent.ParentDeploymentID.Valid {
		return errors.New("fan-out child cannot be a parent")
	}
	if parent.Status == "pending_approval" {
		return nil
	}
	children, err := q.ListDeploymentChildren(
		ctx,
		sql.NullInt64{Int64: deploymentID, Valid: true},
	)
	if err != nil {
		return fmt.Errorf("list existing children: %w", err)
	}
	if len(children) > 0 {
		if len(children) != len(agents) {
			return errors.New("incomplete fan-out expansion")
		}
		return nil
	}
	for _, agent := range agents {
		child, err := q.CreateRoutingChildDeployment(
			ctx,
			db.CreateRoutingChildDeploymentParams{
				ReleaseID:     parent.ReleaseID,
				EnvironmentID: parent.EnvironmentID,
				Status:        "pending", Forced: parent.Forced, Note: parent.Note,
				ParentDeploymentID: sql.NullInt64{
					Int64: deploymentID, Valid: true,
				},
				TargetAgentID: sql.NullString{
					String: agent.ID,
					Valid:  true,
				},
				TargetAgentName: sql.NullString{
					String: agent.Name, Valid: true,
				},
			},
		)
		if err != nil {
			return fmt.Errorf("create child deployment: %w", err)
		}
		ciphertext, err := d.buildPayload(ctx, q, child)
		if err != nil {
			return err
		}
		if _, err := q.CreateDeploymentPayload(
			ctx,
			db.CreateDeploymentPayloadParams{
				DeploymentID: child.ID, Ciphertext: ciphertext,
			},
		); err != nil {
			return fmt.Errorf("create child payload: %w", err)
		}
		if _, err := q.CreateDirectDeploymentDispatch(
			ctx,
			db.CreateDirectDeploymentDispatchParams{
				DeploymentID: child.ID,
				AssignedAgentID: sql.NullString{
					String: agent.ID, Valid: true,
				},
			},
		); err != nil {
			return fmt.Errorf("create child dispatch: %w", err)
		}
	}
	return nil
}
