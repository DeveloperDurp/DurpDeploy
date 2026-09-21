package repository

import (
	"context"
	"database/sql"
	"strings"

	"durpdeploy/internal/db"
)

type DeploymentStepSnapshot struct {
	db.CreateDeploymentStepParams
	Selectors []string
}

func (r *Repository) SnapshotDeploymentSteps(
	ctx context.Context, deploymentID int64,
	steps []DeploymentStepSnapshot,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		return snapshotDeploymentSteps(ctx, q, deploymentID, steps)
	})
}

func snapshotDeploymentSteps(
	ctx context.Context,
	q *db.Queries,
	deploymentID int64,
	steps []DeploymentStepSnapshot,
) error {
	return snapshotDeploymentStepsFromSource(ctx, q, deploymentID, steps, "")
}

func snapshotDeploymentStepsFromSource(
	ctx context.Context,
	q *db.Queries,
	deploymentID int64,
	steps []DeploymentStepSnapshot,
	stepsJSON string,
) error {
	n, err := q.LockDeploymentSnapshot(ctx, deploymentID)
	if err != nil {
		return err
	}
	if n == 0 || deploymentID <= 0 {
		return sql.ErrNoRows
	}
	for i, step := range steps {
		step.DeploymentID, step.StepIndex = deploymentID, int64(i)
		if _, err := q.CreateDeploymentStep(
			ctx,
			step.CreateDeploymentStepParams,
		); err != nil {
			return err
		}
		labels := make(map[string]struct{}, len(step.Selectors))
		for _, label := range step.Selectors {
			label = strings.ToLower(strings.TrimSpace(label))
			if _, exists := labels[label]; exists {
				continue
			}
			labels[label] = struct{}{}
			if _, err := q.AddDeploymentStepSelector(
				ctx,
				db.AddDeploymentStepSelectorParams{
					DeploymentID: deploymentID,
					StepIndex:    int64(i),
					Label:        label,
				},
			); err != nil {
				return err
			}
		}
	}
	if stepsJSON == "" {
		n, err = q.FreezeDeploymentStepSource(ctx, deploymentID)
	} else {
		n, err = q.FreezeDeploymentStepSourceJSON(
			ctx,
			db.FreezeDeploymentStepSourceJSONParams{
				StepsJson: stepsJSON, DeploymentID: deploymentID,
			},
		)
	}
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
