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
	return r.validateReleaseSteps(ctx, releaseID, false)
}

// ValidateRefreshableRelease retains structural and legacy checks without
// requiring the replaced snapshot to fit the current execution maxima.
func (r *Repository) ValidateRefreshableRelease(
	ctx context.Context,
	releaseID int64,
) error {
	return r.validateReleaseSteps(ctx, releaseID, true)
}

func (r *Repository) validateReleaseSteps(
	ctx context.Context,
	releaseID int64,
	forRefresh bool,
) error {
	release, err := r.Queries.GetRelease(ctx, releaseID)
	if err != nil {
		return fmt.Errorf("get release: %w", err)
	}
	if _, err := r.decodeDeploymentSteps(release.StepsJson, forRefresh); err != nil {
		return fmt.Errorf("validate release steps: %w", err)
	}
	return nil
}
