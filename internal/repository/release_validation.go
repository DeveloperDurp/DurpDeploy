package repository

import (
	"context"
	"fmt"
)

// ValidateExecutableRelease checks the stored snapshot without creating a deployment.
func (r *Repository) ValidateExecutableRelease(
	ctx context.Context,
	releaseID int64,
) error {
	release, err := r.Queries.GetRelease(ctx, releaseID)
	if err != nil {
		return fmt.Errorf("get release: %w", err)
	}
	if _, err := deploymentStepsFromRelease(release.StepsJson); err != nil {
		return fmt.Errorf("validate release steps: %w", err)
	}
	return nil
}
