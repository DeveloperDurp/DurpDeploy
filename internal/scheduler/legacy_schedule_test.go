package scheduler_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestTick_LegacyLocalRelease_DisablesWithReasonWithoutDeployment(
	t *testing.T,
) {
	f := newFixture(t)
	project := f.createProject()
	release, err := f.repo.Queries.CreateRelease(
		f.ctx(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "legacy",
			StepsJson: `[{"name":"legacy-step","script_body":"true"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment := f.createEnvironment("legacy-env")
	schedule := f.createSchedule(project.ID, release.ID, environment.ID,
		"* * * * *", f.now.Add(-time.Minute), 1, "legacy")
	calls := f.captureRunCalls()

	f.sched.Tick(f.ctx())
	f.sched.Tick(f.ctx())

	stored, err := f.repo.Queries.GetScheduledDeployment(f.ctx(), schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled != 0 || stored.NextRunAt != schedule.NextRunAt ||
		!strings.Contains(
			stored.LastError,
			"recreate the step and create a new release",
		) {
		t.Fatalf(
			"legacy schedule not disabled with actionable reason: %+v",
			stored,
		)
	}
	deployments, err := f.repo.Queries.ListDeploymentsByRelease(
		f.ctx(),
		release.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 0 || len(calls) != 0 {
		t.Fatalf("legacy schedule created %d deployments and %d run calls",
			len(deployments), len(calls))
	}

	reset, err := f.repo.Queries.ToggleScheduledDeploymentEnabled(
		f.ctx(),
		schedule.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Enabled != 1 || reset.LastError != "" {
		t.Fatalf("re-enabled schedule retained stale error: %+v", reset)
	}
}

func TestTick_AgentOnlyRelease_RemainsEnabledAndFires(t *testing.T) {
	f := newFixture(t)
	project := f.createProject()
	release, err := f.repo.Queries.CreateRelease(
		f.ctx(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "agent-only",
			StepsJson: `[{"name":"remote","script_body":"true","execution_target":"agent"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment := f.createEnvironment("agent-only-env")
	schedule := f.createSchedule(project.ID, release.ID, environment.ID,
		"* * * * *", f.now.Add(-time.Minute), 1, "agent")
	calls := f.captureRunCalls()

	f.sched.Tick(f.ctx())

	stored, err := f.repo.Queries.GetScheduledDeployment(f.ctx(), schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	deployments, err := f.repo.Queries.ListDeploymentsByRelease(
		f.ctx(),
		release.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled != 1 || stored.LastError != "" ||
		stored.NextRunAt <= f.now.Unix() || len(deployments) != 1 {
		t.Fatalf("agent-only schedule failed: schedule=%+v deployments=%d",
			stored, len(deployments))
	}
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("agent-only schedule did not start")
	}
}

func TestRunbookSchedule_LegacyLocalVersion_DisablesWithReasonWithoutExecution(
	t *testing.T,
) {
	f := newFixture(t)
	project := f.createProject()
	environment := f.createEnvironment("legacy-runbook-env")
	book, version, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID, Name: "legacy-runbook",
		StepsJSON: `[{"name":"legacy-step","script_body":"true"}]`,
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
	calls := f.captureRunCalls()

	f.sched.Tick(f.ctx())
	f.sched.Tick(f.ctx())

	stored, err := f.repo.Queries.GetRunbookSchedule(f.ctx(),
		db.GetRunbookScheduleParams{ID: schedule.ID, RunbookID: book.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled != 0 || stored.NextRunAt != schedule.NextRunAt ||
		!strings.Contains(
			stored.LastError,
			"save a new runbook version with a container image",
		) {
		t.Fatalf(
			"legacy runbook schedule not disabled with reason: %+v",
			stored,
		)
	}
	executions, err := f.repo.Queries.ListRunbookExecutions(f.ctx(),
		db.ListRunbookExecutionsParams{ProjectID: project.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 0 || len(calls) != 0 {
		t.Fatalf("legacy runbook created %d executions and %d run calls",
			len(executions), len(calls))
	}
	deployments, err := f.repo.Queries.ListDeploymentsByRelease(
		f.ctx(),
		version.ReleaseID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 0 {
		t.Fatalf("legacy runbook created %d deployments", len(deployments))
	}
}

func TestRunbookSchedule_AgentOnlyVersion_RemainsEnabledAndExecutes(
	t *testing.T,
) {
	f := newFixture(t)
	project := f.createProject()
	environment := f.createEnvironment("agent-runbook-env")
	book, version, err := f.repo.SaveRunbook(f.ctx(), repository.RunbookSave{
		ProjectID: project.ID,
		Name:      "agent-runbook",
		StepsJSON: `[{"name":"agent-step","script_body":"true","execution_target":"agent"}]`,
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
	calls := make(chan struct{}, 1)
	f.sched.SetRunFunc(func(context.Context, int64, int64, int64) {
		calls <- struct{}{}
	})

	f.sched.Tick(f.ctx())

	stored, err := f.repo.Queries.GetRunbookSchedule(f.ctx(),
		db.GetRunbookScheduleParams{ID: schedule.ID, RunbookID: book.ID})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := f.repo.Queries.ListRunbookExecutions(f.ctx(),
		db.ListRunbookExecutionsParams{ProjectID: project.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled != 1 || stored.LastError != "" ||
		stored.NextRunAt <= f.now.Unix() || len(executions) != 1 ||
		executions[0].RunbookVersionID != version.ID {
		t.Fatalf("agent-only runbook failed: schedule=%+v executions=%+v",
			stored, executions)
	}
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("agent-only runbook did not start")
	}
}
