package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/gate"
	"durpdeploy/internal/repository"

	"github.com/robfig/cron/v3"
)

// Scheduler fires due scheduled deployments on a fixed interval.
type Scheduler struct {
	repo         *repository.Repository
	dispatchFunc func(ctx context.Context, deploymentID int64) error
	interval     time.Duration
	now          func() time.Time
	parser       cron.Parser
	log          *slog.Logger
	wg           sync.WaitGroup
	cancel       context.CancelFunc
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithNow sets the clock function used by the scheduler (tests override this).
func WithNow(fn func() time.Time) Option {
	return func(s *Scheduler) { s.now = fn }
}

// WithInterval sets the tick interval (default 60s).
func WithInterval(d time.Duration) Option {
	return func(s *Scheduler) { s.interval = d }
}

// WithLogger sets the slog logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *Scheduler) { s.log = l }
}

// New creates a Scheduler with a 60s tick interval and the standard 5-field cron parser.
func New(
	repo *repository.Repository,
	dispatcher *dispatch.Dispatcher,
	opts ...Option,
) *Scheduler {
	s := &Scheduler{
		repo:         repo,
		dispatchFunc: dispatcher.Dispatch,
		interval:     60 * time.Second,
		now:          time.Now,
		parser: cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
		),
		log: slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SetRunFunc replaces dispatch for tests that assert scheduled deployment inputs.
func (s *Scheduler) SetRunFunc(
	fn func(ctx context.Context, deploymentID, releaseID, environmentID int64),
) {
	s.dispatchFunc = func(ctx context.Context, deploymentID int64) error {
		deployment, err := s.repo.Queries.GetDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		fn(ctx, deploymentID, deployment.ReleaseID, deployment.EnvironmentID)
		return nil
	}
}

// Start begins the background ticker goroutine. Call Stop to shut it down cleanly.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		s.log.Info("scheduler started", "interval", s.interval.String())
		for {
			select {
			case <-ctx.Done():
				s.log.Info("scheduler stopped")
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

// Stop cancels the ticker and waits for the goroutine to exit.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// Tick is exposed for tests so they can trigger a single evaluation without sleeping.
func (s *Scheduler) Tick(ctx context.Context) {
	s.tick(ctx)
}

func (s *Scheduler) tick(ctx context.Context) {
	due, err := s.repo.Queries.ListDueScheduledDeployments(ctx, s.now().Unix())
	if err != nil {
		s.log.Error("list due scheduled deployments", "error", err)
		return
	}
	for _, row := range due {
		s.fireOne(ctx, row)
	}
}

func (s *Scheduler) fireOne(ctx context.Context, row db.ScheduledDeployment) {
	schedule, err := s.parser.Parse(row.Cron)
	if err != nil {
		s.park(ctx, row, fmt.Sprintf("invalid cron: %v", err))
		return
	}

	next := schedule.Next(s.now())
	if next.IsZero() {
		s.park(ctx, row, "invalid cron: unsatisfiable")
		return
	}

	// overlap check
	overlap, err := s.repo.Queries.GetLatestDeploymentForReleaseEnv(
		ctx,
		db.GetLatestDeploymentForReleaseEnvParams{
			ReleaseID:     row.ReleaseID,
			EnvironmentID: row.EnvironmentID,
		},
	)
	if err != nil && err != sql.ErrNoRows {
		s.log.Error(
			"overlap check failed",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"error",
			err,
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error(
				"advance failed",
				"schedule_id",
				row.ID,
				"project_id",
				row.ProjectID,
				"error",
				err,
			)
		}
		return
	}
	if err == nil && overlap.Status == "running" {
		s.log.Info(
			"skipped_overlap",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"reason",
			"running deployment exists",
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error(
				"advance failed",
				"schedule_id",
				row.ID,
				"project_id",
				row.ProjectID,
				"error",
				err,
			)
		}
		return
	}

	// gate check
	project, err := s.repo.Queries.GetProject(ctx, row.ProjectID)
	if err != nil {
		s.log.Error(
			"gate check failed: get project",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"error",
			err,
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error(
				"advance failed",
				"schedule_id",
				row.ID,
				"project_id",
				row.ProjectID,
				"error",
				err,
			)
		}
		return
	}
	release, err := s.repo.Queries.GetRelease(ctx, row.ReleaseID)
	if err != nil {
		s.log.Error(
			"gate check failed: get release",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"error",
			err,
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error(
				"advance failed",
				"schedule_id",
				row.ID,
				"project_id",
				row.ProjectID,
				"error",
				err,
			)
		}
		return
	}
	if release.ProjectID != project.ID {
		s.log.Error(
			"gate check failed: release belongs to another project",
			"schedule_id", row.ID,
			"project_id", row.ProjectID,
			"release_id", row.ReleaseID,
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error("advance failed", "schedule_id", row.ID, "error", err)
		}
		return
	}
	// Combines the deployability gate and the requires-approval check
	// (same lifecycle-stage gate the manual deploy handler enforces) into
	// a single call so the lifecycle stages are only loaded once.
	blocked, reason, requiresApproval, err := gate.CheckAndApproval(
		ctx,
		s.repo,
		project,
		release,
		row.EnvironmentID,
	)
	if err != nil {
		s.log.Error(
			"gate check failed",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"error",
			err,
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error(
				"advance failed",
				"schedule_id",
				row.ID,
				"project_id",
				row.ProjectID,
				"error",
				err,
			)
		}
		return
	}
	if blocked {
		s.log.Info(
			"skipped_gate",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"reason",
			reason,
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error(
				"advance failed",
				"schedule_id",
				row.ID,
				"project_id",
				row.ProjectID,
				"error",
				err,
			)
		}
		return
	}
	initialStatus := "pending"
	if requiresApproval {
		initialStatus = "pending_approval"
	}

	// create deployment
	note := fmt.Sprintf("Scheduled: %d - %s", row.ID, row.Note.String)
	deployment, err := s.repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     row.ReleaseID,
			EnvironmentID: row.EnvironmentID,
			Status:        initialStatus,
			StartedAt:     sql.NullInt64{},
			FinishedAt:    sql.NullInt64{},
			Forced:        0,
			Note:          sql.NullString{String: note, Valid: true},
		},
	)
	if err != nil {
		s.log.Error(
			"create deployment failed",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"error",
			err,
		)
		if err := s.advance(ctx, row, next); err != nil {
			s.log.Error(
				"advance failed",
				"schedule_id",
				row.ID,
				"project_id",
				row.ProjectID,
				"error",
				err,
			)
		}
		return
	}

	s.log.Info(
		"fired",
		"schedule_id",
		row.ID,
		"project_id",
		row.ProjectID,
		"deployment_id",
		deployment.ID,
		"status",
		initialStatus,
	)

	if initialStatus == "pending" {
		if err := s.dispatchFunc(ctx, deployment.ID); err != nil {
			s.log.Error(
				"dispatch scheduled deployment failed",
				"schedule_id",
				row.ID,
				"deployment_id",
				deployment.ID,
				"error",
				err,
			)
		}
	}

	if err := s.advance(ctx, row, next); err != nil {
		s.log.Error(
			"advance failed",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"error",
			err,
		)
	}
}

func (s *Scheduler) park(
	ctx context.Context,
	row db.ScheduledDeployment,
	reason string,
) {
	parkedAt := s.now().Add(10 * 365 * 24 * time.Hour).Unix()
	if err := s.repo.Queries.UpdateScheduledDeploymentNextRun(
		ctx,
		db.UpdateScheduledDeploymentNextRunParams{
			NextRunAt: parkedAt,
			ID:        row.ID,
		},
	); err != nil {
		s.log.Error(
			"park failed",
			"schedule_id",
			row.ID,
			"project_id",
			row.ProjectID,
			"error",
			err,
		)
	}
	s.log.Info(
		"parked",
		"schedule_id",
		row.ID,
		"project_id",
		row.ProjectID,
		"reason",
		reason,
	)
}

func (s *Scheduler) advance(
	ctx context.Context,
	row db.ScheduledDeployment,
	next time.Time,
) error {
	return s.repo.Queries.UpdateScheduledDeploymentNextRun(
		ctx,
		db.UpdateScheduledDeploymentNextRunParams{
			NextRunAt: next.Unix(),
			ID:        row.ID,
		},
	)
}
