package scheduler_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunbookSchedule_LatestAndPinnedResolveConcreteVersions(t *testing.T) {
	f := newFixture(t)
	project := f.createProject()
	environment := f.createEnvironment("runbook-env")
	book, first, err := f.repo.SaveRunbook(
		context.Background(),
		repository.RunbookSave{
			ProjectID: project.ID,
			Name:      "maintenance",
			StepsJSON: `[{"name":"check","script_body":"echo first","interpreter":"bash"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	latestSchedule, err := f.repo.Queries.CreateRunbookSchedule(f.ctx(),
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron: "* * * * *", NextRunAt: f.now.Unix(),
		})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID,
		RunbookID: book.ID,
		StepsJSON: `[{"name":"check","script_body":"echo second","interpreter":"bash"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	pinnedSchedule, err := f.repo.Queries.CreateRunbookSchedule(f.ctx(),
		db.CreateRunbookScheduleParams{
			RunbookID:     book.ID,
			VersionID:     sql.NullInt64{Int64: first.ID, Valid: true},
			EnvironmentID: environment.ID,
			Cron:          "* * * * *",
			NextRunAt:     f.now.Unix(),
		})
	if err != nil {
		t.Fatal(err)
	}
	f.sched.SetRunFunc(func(context.Context, int64, int64, int64) {})
	f.sched.Tick(f.ctx())
	f.sched.Tick(f.ctx())
	executions, err := f.repo.Queries.ListRunbookExecutions(
		f.ctx(),
		db.ListRunbookExecutionsParams{ProjectID: project.ID, Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 2 {
		t.Fatalf("executions=%d want 2", len(executions))
	}
	versionsBySchedule := map[int64]int64{}
	for _, execution := range executions {
		versionsBySchedule[execution.ScheduleID.Int64] = execution.RunbookVersionID
	}
	if versionsBySchedule[latestSchedule.ID] != second.ID ||
		versionsBySchedule[pinnedSchedule.ID] != first.ID {
		t.Fatalf("resolved versions=%v want latest=%d pinned=%d",
			versionsBySchedule, second.ID, first.ID)
	}
}

func TestRunbookSchedule_ApprovalHoldsExecution(t *testing.T) {
	f := newFixture(t)
	project := f.createProject()
	environment := f.createEnvironment("approval-env")
	book, _, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID, Name: "approval-book",
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := f.repo.Queries.CreateLifecycle(f.ctx(),
		db.CreateLifecycleParams{Name: "approval-lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.SetProjectLifecycle(f.ctx(),
		db.SetProjectLifecycleParams{
			ID: project.ID,
			LifecycleID: sql.NullInt64{
				Int64: lifecycle.ID, Valid: true,
			},
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateLifecycleStage(f.ctx(),
		db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: environment.ID,
			RequiresApproval: 1,
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateRunbookSchedule(f.ctx(),
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron: "* * * * *", NextRunAt: f.now.Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	run := make(chan struct{}, 1)
	f.sched.SetRunFunc(func(context.Context, int64, int64, int64) {
		run <- struct{}{}
	})
	f.sched.Tick(f.ctx())
	executions, err := f.repo.Queries.ListRunbookExecutions(f.ctx(),
		db.ListRunbookExecutionsParams{ProjectID: project.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 1 || executions[0].Status != "pending_approval" {
		t.Fatalf("executions=%+v", executions)
	}
	select {
	case <-run:
		t.Fatal("approval-required runbook started")
	default:
	}
}

func TestRunbookSchedule_InvalidCronDisablesSchedule(t *testing.T) {
	f := newFixture(t)
	project := f.createProject()
	environment := f.createEnvironment("invalid-cron-env")
	book, _, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID, Name: "invalid-cron-book",
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := f.repo.Queries.CreateRunbookSchedule(f.ctx(),
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron: "invalid", NextRunAt: f.now.Unix(),
		})
	if err != nil {
		t.Fatal(err)
	}
	f.sched.Tick(f.ctx())
	stored, err := f.repo.Queries.GetRunbookSchedule(f.ctx(),
		db.GetRunbookScheduleParams{ID: schedule.ID, RunbookID: book.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled != 0 {
		t.Fatalf("invalid schedule still enabled: %+v", stored)
	}
}

func TestRunbookSchedule_ActiveExecutionSkipsNextOccurrence(t *testing.T) {
	f := newFixture(t)
	project := f.createProject()
	environment := f.createEnvironment("overlap-env")
	book, _, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID, Name: "overlap-book",
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := f.repo.Queries.CreateRunbookSchedule(f.ctx(),
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron: "* * * * *", NextRunAt: f.now.Unix(),
		})
	if err != nil {
		t.Fatal(err)
	}
	f.sched.SetRunFunc(func(context.Context, int64, int64, int64) {})
	f.sched.Tick(f.ctx())
	stored, err := f.repo.Queries.GetRunbookSchedule(f.ctx(),
		db.GetRunbookScheduleParams{ID: schedule.ID, RunbookID: book.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.repo.CreateRunbookExecution(f.ctx(),
		repository.RunbookExecutionRequest{
			ProjectID:     project.ID,
			RunbookID:     book.ID,
			EnvironmentID: environment.ID,
			ScheduleID: sql.NullInt64{
				Int64: schedule.ID,
				Valid: true,
			},
			ScheduleExpectedRunAt: stored.NextRunAt,
			ScheduleNextRunAt:     stored.NextRunAt + 60,
			FiredAt:               stored.NextRunAt,
		})
	if !errors.Is(err, repository.ErrRunbookScheduleOverlap) {
		t.Fatalf("overlap error=%v", err)
	}
	stored, err = f.repo.Queries.GetRunbookSchedule(f.ctx(),
		db.GetRunbookScheduleParams{ID: schedule.ID, RunbookID: book.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.NextRunAt != schedule.NextRunAt+120 ||
		stored.LastFiredAt.Int64 != schedule.NextRunAt {
		t.Fatalf("schedule advanced incorrectly: %+v", stored)
	}
	executions, err := f.repo.Queries.ListRunbookExecutions(
		f.ctx(),
		db.ListRunbookExecutionsParams{ProjectID: project.ID, Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 1 {
		t.Fatalf("executions=%d want 1", len(executions))
	}
}

func TestRunbookSchedule_BlockedAndInaccessibleStages(t *testing.T) {
	f := newFixture(t)
	project := f.createProject()
	first := f.createEnvironment("first-stage")
	later := f.createEnvironment("later-stage")
	outside := f.createEnvironment("outside-stage")
	book, _, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID, Name: "gated-book",
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := f.repo.Queries.CreateLifecycle(f.ctx(),
		db.CreateLifecycleParams{Name: "runbook-stages"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.SetProjectLifecycle(f.ctx(),
		db.SetProjectLifecycleParams{
			ID: project.ID,
			LifecycleID: sql.NullInt64{
				Int64: lifecycle.ID,
				Valid: true,
			},
		}); err != nil {
		t.Fatal(err)
	}
	for order, env := range []db.Environment{first, later} {
		if _, err := f.repo.Queries.CreateLifecycleStage(f.ctx(),
			db.CreateLifecycleStageParams{
				LifecycleID: lifecycle.ID, EnvironmentID: env.ID,
				SortOrder: int64(order),
			}); err != nil {
			t.Fatal(err)
		}
	}
	var schedules []db.RunbookSchedule
	for _, env := range []db.Environment{later, outside} {
		schedule, err := f.repo.Queries.CreateRunbookSchedule(f.ctx(),
			db.CreateRunbookScheduleParams{
				RunbookID: book.ID, EnvironmentID: env.ID,
				Cron: "* * * * *", NextRunAt: f.now.Unix(),
			})
		if err != nil {
			t.Fatal(err)
		}
		schedules = append(schedules, schedule)
	}
	f.sched.Tick(f.ctx())
	blocked, err := f.repo.Queries.GetRunbookSchedule(f.ctx(),
		db.GetRunbookScheduleParams{ID: schedules[0].ID, RunbookID: book.ID})
	if err != nil {
		t.Fatal(err)
	}
	inaccessible, err := f.repo.Queries.GetRunbookSchedule(f.ctx(),
		db.GetRunbookScheduleParams{ID: schedules[1].ID, RunbookID: book.ID})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.NextRunAt <= f.now.Unix() || blocked.Enabled == 0 {
		t.Fatalf("blocked schedule did not advance: %+v", blocked)
	}
	if inaccessible.Enabled != 0 {
		t.Fatalf("inaccessible schedule remains enabled: %+v", inaccessible)
	}
	executions, err := f.repo.Queries.ListRunbookExecutions(
		f.ctx(),
		db.ListRunbookExecutionsParams{ProjectID: project.ID, Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 0 {
		t.Fatalf("gated schedules created %d executions", len(executions))
	}
}
