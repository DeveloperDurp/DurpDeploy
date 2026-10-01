package repository

import (
	"context"
	"errors"

	"durpdeploy/internal/db"
)

type RunbookSave struct {
	ProjectID         int64
	RunbookID         int64
	Name              string
	Description       string
	StepsJSON         string
	ArtifactReleaseID int64
	KeepArtifactPin   bool
}

var ErrProjectHasActiveRunbook = errors.New(
	"project has active or unconfirmed deployments",
)

var ErrEnvironmentHasActiveDeployment = errors.New(
	"environment has active or unconfirmed deployments",
)

func (r *Repository) DeleteEnvironment(ctx context.Context, id int64) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			active, err := q.HasActiveEnvironmentDeployment(ctx, id)
			if err != nil {
				return err
			}
			if active != 0 {
				return ErrEnvironmentHasActiveDeployment
			}
			deployments, err := q.ListEnvironmentDeploymentIDs(ctx, id)
			if err != nil {
				return err
			}
			for _, deploymentID := range deployments {
				if err := deleteDeploymentHistory(
					ctx,
					q,
					deploymentID,
				); err != nil {
					return err
				}
			}
			return q.DeleteEnvironment(ctx, id)
		})
	})
}

func (r *Repository) DeleteProject(ctx context.Context, projectID int64) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.LockProject(ctx, projectID); err != nil {
				return err
			}
			active, err := q.HasActiveProjectDeployment(
				ctx,
				projectID,
			)
			if err != nil {
				return err
			}
			if active != 0 {
				return ErrProjectHasActiveRunbook
			}
			deployments, err := q.ListProjectDeploymentIDs(
				ctx,
				projectID,
			)
			if err != nil {
				return err
			}
			for _, deploymentID := range deployments {
				if err := deleteDeploymentHistory(
					ctx,
					q,
					deploymentID,
				); err != nil {
					return err
				}
			}
			if err := q.DeleteProjectRunbookExecutions(
				ctx,
				projectID,
			); err != nil {
				return err
			}
			if err := q.DeleteProjectRunbookSchedules(
				ctx,
				projectID,
			); err != nil {
				return err
			}
			if err := q.DeleteProjectRunbookVersions(
				ctx,
				projectID,
			); err != nil {
				return err
			}
			for _, cleanup := range []func(context.Context, int64) error{q.ClearArtifactSourceRelease, q.DeleteProjectDeploymentArtifacts, q.DeleteProjectReleaseArtifacts, q.DeleteProjectArtifactRepository, q.DeleteProjectPackageRepositories} {
				if err := cleanup(ctx, projectID); err != nil {
					return err
				}
			}
			if err := q.DeleteProjectRunbookReleases(
				ctx,
				projectID,
			); err != nil {
				return err
			}
			return q.DeleteProject(ctx, projectID)
		})
	})
}

func deleteDeploymentHistory(
	ctx context.Context,
	q *db.Queries,
	deploymentID int64,
) error {
	if err := q.DeleteDeploymentArtifact(ctx, deploymentID); err != nil {
		return err
	}
	for _, deletePart := range []func(context.Context, int64) error{
		q.DeleteRunbookRemoteStepLogSequences,
		q.DeleteRunbookRemoteStepRuns,
		q.DeleteRunbookLogScopes,
		q.DeleteRunbookDispatches,
		q.DeleteRunbookStepAttempts,
		q.DeleteRunbookStepSelectors,
		q.DeleteRunbookSteps,
		q.DeleteRunbookStepSource,
		q.DeleteRunbookRemoteClaim,
	} {
		if err := deletePart(ctx, deploymentID); err != nil {
			return err
		}
	}
	return q.DeleteDeployment(ctx, deploymentID)
}
