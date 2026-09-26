package repository

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

type RunbookSave struct {
	ProjectID   int64
	RunbookID   int64
	Name        string
	Description string
	StepsJSON   string
}

var ErrProjectHasActiveRunbook = errors.New(
	"project has active runbook executions",
)

func (r *Repository) DeleteProject(ctx context.Context, projectID int64) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			active, err := q.HasActiveProjectRunbookExecution(
				ctx,
				projectID,
			)
			if err != nil {
				return err
			}
			if active != 0 {
				return ErrProjectHasActiveRunbook
			}
			deployments, err := q.ListProjectRunbookDeploymentIDs(
				ctx,
				projectID,
			)
			if err != nil {
				return err
			}
			for _, deploymentID := range deployments {
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
				if err := q.DeleteDeployment(ctx, deploymentID); err != nil {
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

func (r *Repository) SaveRunbook(
	ctx context.Context,
	arg RunbookSave,
) (db.Runbook, db.RunbookVersion, error) {
	variables, err := r.ListVariablesByProject(ctx, arg.ProjectID)
	if err != nil {
		return db.Runbook{}, db.RunbookVersion{}, fmt.Errorf(
			"list variables: %w",
			err,
		)
	}
	for i := range variables {
		variables[i].Value, err = r.EncryptValue(variables[i].Value)
		if err != nil {
			return db.Runbook{}, db.RunbookVersion{}, fmt.Errorf(
				"encrypt variable: %w",
				err,
			)
		}
	}
	var runbook db.Runbook
	var version db.RunbookVersion
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			var txErr error
			if arg.RunbookID == 0 {
				runbook, txErr = q.CreateRunbook(ctx, db.CreateRunbookParams{
					ProjectID: arg.ProjectID, Name: arg.Name,
					Description: arg.Description,
				})
			} else {
				runbook, txErr = q.GetRunbook(ctx, db.GetRunbookParams{
					ID: arg.RunbookID, ProjectID: arg.ProjectID,
				})
			}
			if txErr != nil {
				return txErr
			}
			number := int64(1)
			if arg.RunbookID != 0 {
				latest, latestErr := q.GetLatestRunbookVersion(ctx, runbook.ID)
				if latestErr != nil {
					return latestErr
				}
				number = latest.Version + 1
			}
			var releaseToken [16]byte
			if _, err := rand.Read(releaseToken[:]); err != nil {
				return err
			}
			release, releaseErr := q.CreateRelease(ctx, db.CreateReleaseParams{
				ProjectID: arg.ProjectID,
				Version: fmt.Sprintf(
					"runbook:%d:%d:%x",
					runbook.ID,
					number,
					releaseToken,
				),
				StepsJson: arg.StepsJSON,
			})
			if releaseErr != nil {
				return releaseErr
			}
			if err := q.SetRunbookReleaseKind(ctx, release.ID); err != nil {
				return err
			}
			for _, variable := range variables {
				_, err := q.CreateReleaseVariable(
					ctx,
					db.CreateReleaseVariableParams{
						ReleaseID:     release.ID,
						Name:          variable.Name,
						Value:         variable.Value,
						EnvironmentID: variable.EnvironmentID,
						Secret:        variable.Secret,
					},
				)
				if err != nil {
					return err
				}
			}
			version, txErr = q.CreateRunbookVersion(ctx,
				db.CreateRunbookVersionParams{
					RunbookID: runbook.ID,
					Version:   number,
					ReleaseID: release.ID,
				})
			return txErr
		})
	})
	if err != nil {
		return db.Runbook{}, db.RunbookVersion{}, fmt.Errorf(
			"save runbook: %w",
			err,
		)
	}
	return runbook, version, nil
}
