package repository

import (
	"context"
	"fmt"
	"slices"

	"durpdeploy/internal/db"
)

func (r *Repository) ProjectPanelDeployments(
	ctx context.Context, projectIDs, environmentIDs []int64,
) ([]db.ListProjectPanelDeploymentsRow, error) {
	var summaries []db.ListProjectPanelDeploymentsRow
	for projects := range slices.Chunk(projectIDs, 500) {
		for environments := range slices.Chunk(environmentIDs, 500) {
			rows, err := r.Queries.ListProjectPanelDeployments(ctx,
				db.ListProjectPanelDeploymentsParams{
					ProjectIds: projects, EnvironmentIds: environments,
				})
			if err != nil {
				return nil, fmt.Errorf(
					"load project panel deployments: %w",
					err,
				)
			}
			summaries = append(summaries, rows...)
		}
	}
	return summaries, nil
}
