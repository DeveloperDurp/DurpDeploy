package scheduler_test

import (
	"context"
	"database/sql"
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
	executions, err := f.repo.Queries.ListRunbookExecutions(f.ctx(), project.ID)
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
