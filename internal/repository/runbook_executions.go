package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
)

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
}

var ErrRunbookScheduleConflict = errors.New("runbook schedule already fired")

var ErrRunbookScheduleOverlap = errors.New(
	"runbook schedule execution still active",
)
var ErrRunbookGate = errors.New("runbook lifecycle gate blocked execution")

func (r *Repository) CreateRunbookExecution(
	ctx context.Context,
	arg RunbookExecutionRequest,
) (db.RunbookExecution, DeploymentResult, error) {
	var execution db.RunbookExecution
	var result DeploymentResult
	var skipped bool
	err := withSQLiteBusyRetry(ctx, func() error {
		skipped = false
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.GetRunbook(ctx, db.GetRunbookParams{
				ID: arg.RunbookID, ProjectID: arg.ProjectID,
			}); err != nil {
				return err
			}
			if arg.ScheduleID.Valid {
				active, err := q.HasActiveRunbookScheduleExecution(
					ctx,
					arg.ScheduleID,
				)
				if err != nil {
					return err
				}
				if active != 0 {
					changed, err := q.SkipRunbookSchedule(ctx,
						db.SkipRunbookScheduleParams{
							NextRunAt:   arg.ScheduleNextRunAt,
							ID:          arg.ScheduleID.Int64,
							NextRunAt_2: arg.ScheduleExpectedRunAt,
						})
					if err != nil {
						return err
					}
					if changed != 1 {
						return ErrRunbookScheduleConflict
					}
					skipped = true
					return nil
				}
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
			project, err := q.GetProject(ctx, arg.ProjectID)
			if err != nil {
				return err
			}
			release, err := q.GetRelease(ctx, version.ReleaseID)
			if err != nil {
				return err
			}
			blocked, reason, approval, err := gate.CheckAndApproval(
				ctx, q, project, release, arg.EnvironmentID,
			)
			if err != nil {
				return err
			}
			if blocked {
				return fmt.Errorf("%w: %s", ErrRunbookGate, reason)
			}
			status := "pending"
			if approval {
				status = "pending_approval"
			}
			result, err = r.createDeployment(ctx, q, db.CreateDeploymentParams{
				ReleaseID: version.ReleaseID, EnvironmentID: arg.EnvironmentID,
				Status: status,
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
	if skipped {
		return db.RunbookExecution{}, DeploymentResult{},
			ErrRunbookScheduleOverlap
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
