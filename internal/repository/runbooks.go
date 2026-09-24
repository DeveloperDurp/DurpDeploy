package repository

import (
	"context"
	"database/sql"
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

func (r *Repository) DeleteProject(ctx context.Context, projectID int64) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteProjectRunbookExecutions(ctx, projectID); err != nil {
			return err
		}
		if err := q.DeleteProjectRunbookSchedules(ctx, projectID); err != nil {
			return err
		}
		if err := q.DeleteProjectRunbookVersions(ctx, projectID); err != nil {
			return err
		}
		return q.DeleteProject(ctx, projectID)
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
			release, releaseErr := q.CreateRelease(ctx, db.CreateReleaseParams{
				ProjectID: arg.ProjectID,
				Version:   fmt.Sprintf("runbook:%d:%d", runbook.ID, number),
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

type RunbookExecutionRequest struct {
	ProjectID             int64
	RunbookID             int64
	VersionID             int64
	EnvironmentID         int64
	ActorUserID           sql.NullInt64
	ScheduleID            sql.NullInt64
	ScheduleNextRunAt     int64
	ScheduleExpectedRunAt int64
	FiredAt               int64
	Status                string
}

var ErrRunbookScheduleConflict = errors.New("runbook schedule already fired")

func (r *Repository) CreateRunbookExecution(
	ctx context.Context,
	arg RunbookExecutionRequest,
) (db.RunbookExecution, DeploymentResult, error) {
	var execution db.RunbookExecution
	var result DeploymentResult
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.GetRunbook(ctx, db.GetRunbookParams{
				ID: arg.RunbookID, ProjectID: arg.ProjectID,
			}); err != nil {
				return err
			}
			if arg.ScheduleID.Valid {
				changed, err := q.AdvanceRunbookSchedule(ctx,
					db.AdvanceRunbookScheduleParams{
						NextRunAt: arg.ScheduleNextRunAt,
						LastFiredAt: sql.NullInt64{
							Int64: arg.FiredAt,
							Valid: true,
						},
						ID:          arg.ScheduleID.Int64,
						NextRunAt_2: arg.ScheduleExpectedRunAt,
					})
				if err != nil {
					return err
				}
				if changed != 1 {
					return ErrRunbookScheduleConflict
				}
			}
			var version db.RunbookVersion
			var err error
			if arg.VersionID == 0 {
				version, err = q.GetLatestRunbookVersion(ctx, arg.RunbookID)
			} else {
				version, err = q.GetRunbookVersion(
					ctx,
					db.GetRunbookVersionParams{
						ID: arg.VersionID, RunbookID: arg.RunbookID,
					},
				)
			}
			if err != nil {
				return err
			}
			result, err = r.createDeployment(ctx, q, db.CreateDeploymentParams{
				ReleaseID: version.ReleaseID, EnvironmentID: arg.EnvironmentID,
				Status: arg.Status,
			})
			if err != nil {
				return err
			}
			if err := q.SetRunbookDeploymentKind(
				ctx,
				result.Deployment.ID,
			); err != nil {
				return err
			}
			result.Deployment.Kind = "runbook"
			execution, err = q.CreateRunbookExecution(ctx,
				db.CreateRunbookExecutionParams{
					RunbookVersionID: version.ID,
					DeploymentID:     result.Deployment.ID,
					ActorUserID:      arg.ActorUserID,
					ScheduleID:       arg.ScheduleID,
				})
			return err
		})
	})
	if err != nil {
		return db.RunbookExecution{}, DeploymentResult{},
			fmt.Errorf("create runbook execution: %w", err)
	}
	return execution, result, nil
}

func (r *Repository) ListRunbookLogs(
	ctx context.Context,
	deploymentID int64,
) ([]db.DeploymentLog, error) {
	logs := make([]db.DeploymentLog, 0)
	err := r.ForEachDeploymentLogByDeploymentAsc(ctx, deploymentID,
		func(log db.DeploymentLog) error {
			logs = append(logs, log)
			return nil
		})
	return logs, err
}
