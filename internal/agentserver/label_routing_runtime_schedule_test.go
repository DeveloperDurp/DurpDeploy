package agentserver

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
)

func (f *labelRuntimeFixture) schedule(
	t *testing.T,
	strategy string,
) dispatch.ScheduledRequest {
	t.Helper()
	schedule, err := f.repo.Queries.CreateScheduledDeployment(t.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID: f.project.ID, ReleaseID: f.release.ID,
			EnvironmentID: f.environment.ID, Cron: "* * * * *",
			NextRunAt: 100, Enabled: 1,
		})
	runtimeMust(t, err)
	_, err = f.repo.Queries.CreateScheduledDeploymentRoutingPolicy(t.Context(),
		db.CreateScheduledDeploymentRoutingPolicyParams{
			ScheduledDeploymentID: schedule.ID, TargetMode: "label",
			AgentLabelID:  sql.NullInt64{Int64: f.label.ID, Valid: true},
			AgentStrategy: sql.NullString{String: strategy, Valid: true},
		})
	runtimeMust(t, err)
	return dispatch.ScheduledRequest{
		CreateRequest: f.request(strategy),
		ScheduleID:    schedule.ID, DueAt: 100, NextRunAt: 160,
	}
}

func runtimeScheduleCAS(t *testing.T, f *labelRuntimeFixture) {
	request := f.schedule(t, "all")
	start := make(chan struct{})
	results := make(chan error, 3)
	for range 3 {
		service := f.service(f.connection(t))
		go func() {
			<-start
			_, err := service.CreateScheduled(t.Context(), request)
			results <- err
		}()
	}
	close(start)
	winners, conflicts := 0, 0
	for range 3 {
		err := <-results
		switch {
		case err == nil:
			winners++
		case errors.Is(err, dispatch.ErrScheduledOccurrenceClaimed):
			conflicts++
		default:
			t.Fatalf("schedule CAS error=%v", err)
		}
	}
	if winners != 1 || conflicts != 2 {
		t.Fatalf("schedule winners=%d conflicts=%d", winners, conflicts)
	}
	f.assertOccurrence(t, request)
	_, err := f.service(f.connection(t)).CreateScheduled(t.Context(), request)
	if !errors.Is(err, dispatch.ErrScheduledOccurrenceClaimed) {
		t.Fatalf("restarted occurrence error=%v", err)
	}
	if got := f.routingCounts(t); !reflect.DeepEqual(
		got,
		[]int{1, 0, 0, 0, 0, 0},
	) {
		t.Fatalf("scheduled intent counts=%v", got)
	}
	t.Log("occurrence winners=1 conflicts=2 restart_replay=rejected")
}

func (f *labelRuntimeFixture) assertOccurrence(
	t *testing.T,
	request dispatch.ScheduledRequest,
) {
	t.Helper()
	var count int
	runtimeMust(t, f.repo.DB.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM scheduled_deployment_occurrences WHERE scheduled_deployment_id=? AND due_at=?",
		request.ScheduleID,
		request.DueAt,
	).Scan(&count))
	stored, err := f.repo.Queries.GetScheduledDeployment(
		t.Context(),
		request.ScheduleID,
	)
	runtimeMust(t, err)
	if count != 1 || stored.NextRunAt != request.NextRunAt ||
		stored.LastFiredAt.Int64 != request.DueAt {
		t.Fatalf("occurrence count=%d schedule=%v", count, stored)
	}
	t.Logf("SQL occurrence_count=%d next_run=%d last_fired=%d",
		count, stored.NextRunAt, stored.LastFiredAt.Int64)
}

func runtimeImmediateRollback(t *testing.T, f *labelRuntimeFixture) {
	broken := dispatch.NewCreationService(
		f.repo,
		dispatch.New(f.repo, nil, nil),
	)
	for _, strategy := range []string{"round_robin", "all"} {
		_, err := broken.Create(t.Context(), f.request(strategy))
		if err == nil ||
			!strings.Contains(err.Error(), "requires a secret box") {
			t.Fatalf(
				"%s creation accepted missing payload encryption key",
				strategy,
			)
		}
		if got := f.routingCounts(t); !reflect.DeepEqual(
			got,
			[]int{0, 0, 0, 0, 0, 0},
		) {
			t.Fatalf("%s immediate rollback counts=%v", strategy, got)
		}
		t.Logf(
			"immediate strategy=%s failed=true root_and_artifacts_rolled_back=true",
			strategy,
		)
	}
	root := f.create(t, "round_robin")
	f.assertAgents(t, root.ID, f.agents[:1])
}

func runtimeScheduledRecovery(t *testing.T, f *labelRuntimeFixture) {
	for _, strategy := range []string{"round_robin", "all"} {
		request := f.schedule(t, strategy)
		broken := dispatch.NewCreationService(
			f.repo,
			dispatch.New(f.repo, nil, nil),
		)
		root, err := broken.CreateScheduled(t.Context(), request)
		runtimeMust(t, err)
		before := f.routingCounts(t)
		err = broken.DispatchFrozen(t.Context(), root.ID)
		if err == nil ||
			!strings.Contains(err.Error(), "requires a secret box") {
			t.Fatalf(
				"%s dispatch accepted missing payload encryption key",
				strategy,
			)
		}
		if !reflect.DeepEqual(before, f.routingCounts(t)) {
			t.Fatal("post-commit failure left routing artifacts")
		}
		stored, err := f.repo.Queries.GetDeployment(t.Context(), root.ID)
		runtimeMust(t, err)
		if stored != root {
			t.Fatal("post-commit failure changed recoverable root")
		}
		recoverable, err := f.repo.Queries.ListRecoverableScheduledDeployments(
			t.Context(),
		)
		runtimeMust(t, err)
		found := false
		for _, deployment := range recoverable {
			found = found || deployment.ID == root.ID
		}
		if !found {
			t.Fatal("failed dispatch is not recoverable")
		}
		restarted := f.service(f.connection(t))
		runtimeMust(t, restarted.DispatchFrozen(t.Context(), root.ID))
		after := f.routingCounts(t)
		runtimeMust(t, restarted.DispatchFrozen(t.Context(), root.ID))
		if !reflect.DeepEqual(after, f.routingCounts(t)) {
			t.Fatal("restart replay duplicated routing artifacts")
		}
		want := f.agents
		if strategy == "round_robin" {
			want = f.agents[:1]
		}
		f.assertAgents(t, root.ID, want)
		f.assertOccurrence(t, request)
		t.Logf(
			"scheduled strategy=%s post_commit_rollback=true restart_expansion=once",
			strategy,
		)
	}
}
