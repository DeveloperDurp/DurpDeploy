package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
	"durpdeploy/internal/repository"
)

func (s *Scheduler) tickRunbooks(ctx context.Context) {
	due, err := s.repo.Queries.ListDueRunbookSchedules(ctx, s.now().Unix())
	if err != nil {
		s.log.Error("list due runbook schedules", "error", err)
		return
	}
	for _, schedule := range due {
		s.fireRunbook(ctx, schedule)
	}
}

func (s *Scheduler) fireRunbook(ctx context.Context, row db.RunbookSchedule) {
	parsed, err := s.parser.Parse(row.Cron)
	if err != nil {
		s.log.Error("invalid runbook cron", "schedule_id", row.ID, "error", err)
		_ = s.repo.Queries.DisableRunbookSchedule(ctx, row.ID)
		return
	}
	next := parsed.Next(s.now())
	if next.IsZero() {
		s.log.Error("unsatisfiable runbook cron", "schedule_id", row.ID)
		_ = s.repo.Queries.DisableRunbookSchedule(ctx, row.ID)
		return
	}
	book, err := s.repo.Queries.GetRunbookByID(ctx, row.RunbookID)
	if err != nil {
		s.log.Error(
			"read scheduled runbook",
			"schedule_id",
			row.ID,
			"error",
			err,
		)
		return
	}
	project, err := s.repo.Queries.GetProject(ctx, book.ProjectID)
	if err != nil {
		s.log.Error("read runbook project", "schedule_id", row.ID, "error", err)
		return
	}
	if _, err := s.repo.Queries.GetEnvironment(
		ctx,
		row.EnvironmentID,
	); err != nil {
		s.log.Error(
			"read runbook environment",
			"schedule_id",
			row.ID,
			"error",
			err,
		)
		return
	}
	if project.LifecycleID.Valid {
		stages, err := s.repo.Queries.ListLifecycleStages(
			ctx,
			project.LifecycleID.Int64,
		)
		if err != nil {
			s.log.Error(
				"read runbook lifecycle",
				"schedule_id",
				row.ID,
				"error",
				err,
			)
			return
		}
		accessible := false
		for _, stage := range stages {
			accessible = accessible || stage.EnvironmentID == row.EnvironmentID
		}
		if !accessible {
			s.log.Warn(
				"skip inaccessible runbook environment",
				"schedule_id",
				row.ID,
			)
			return
		}
	}
	requiresApproval, err := gate.RequiresApproval(
		ctx,
		s.repo,
		project,
		row.EnvironmentID,
	)
	if err != nil {
		s.log.Error(
			"check runbook approval",
			"schedule_id",
			row.ID,
			"error",
			err,
		)
		return
	}
	status := "pending"
	if requiresApproval {
		status = "pending_approval"
	}
	versionID := int64(0)
	if row.VersionID.Valid {
		versionID = row.VersionID.Int64
	}
	execution, result, err := s.repo.CreateRunbookExecution(ctx,
		repository.RunbookExecutionRequest{
			ProjectID: book.ProjectID, RunbookID: book.ID,
			VersionID: versionID, EnvironmentID: row.EnvironmentID,
			ScheduleID:            sql.NullInt64{Int64: row.ID, Valid: true},
			ScheduleNextRunAt:     next.Unix(),
			ScheduleExpectedRunAt: row.NextRunAt,
			FiredAt:               s.now().Unix(),
			Status:                status,
		})
	if err != nil {
		if errors.Is(err, repository.ErrRunbookScheduleConflict) {
			return
		}
		s.log.Error(
			"create scheduled runbook execution",
			"schedule_id",
			row.ID,
			"error",
			err,
		)
		return
	}
	s.log.Info("runbook fired", slog.Int64("schedule_id", row.ID),
		slog.Int64("execution_id", execution.ID))
	if status == "pending" {
		go s.runFunc(context.Background(), result.Deployment.ID,
			result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	}
}
