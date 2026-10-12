package repository

import (
	"context"
	"fmt"
	"slices"

	"durpdeploy/internal/db"
)

// StepSelectors bounds each query below SQL Server and SQLite parameter limits.
func StepSelectors(
	ctx context.Context,
	queries *db.Queries,
	stepIDs []int64,
) (map[int64][]string, error) {
	selectors := make(map[int64][]string, len(stepIDs))
	for batch := range slices.Chunk(stepIDs, 500) {
		rows, err := queries.ListStepAgentSelectorsByStepIDs(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("list step selectors: %w", err)
		}
		for _, row := range rows {
			selectors[row.StepID] = append(selectors[row.StepID], row.Label)
		}
	}
	return selectors, nil
}
