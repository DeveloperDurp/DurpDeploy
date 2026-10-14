package scheduler_test

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunbookSchedule_DeletedRunbookDoesNotFire(t *testing.T) {
	// Given: a due schedule whose runbook is deleted before the next tick.
	f := newFixture(t)
	project := f.createProject()
	environment := f.createEnvironment("deleted-book")
	book, _, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID, Name: "deleted-book", StepsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.CreateRunbookSchedule(f.ctx(), project.ID,
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron: "* * * * *", NextRunAt: f.now.Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DeleteRunbook(f.ctx(), db.GetRunbookParams{
		ID: book.ID, ProjectID: project.ID,
	}); err != nil {
		t.Fatal(err)
	}
	// When: the scheduler checks due work.
	f.sched.Tick(f.ctx())
	// Then: no execution is created.
	count, err := f.repo.Queries.CountRunbookExecutions(f.ctx(), project.ID)
	if err != nil || count != 0 {
		t.Fatalf("deleted runbook executions=%d error=%v", count, err)
	}
}
