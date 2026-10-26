//go:build e2e

package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/scheduler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

// Pause only non-transactional scheduler transitions. Successful firing still
// uses the real queue transaction, including rollback before invalid-disable.
type scheduleTransitionDB struct {
	*sql.DB
	started, resume chan struct{}
	once            sync.Once
	fail            string
}

func (d *scheduleTransitionDB) ExecContext(
	ctx context.Context, query string, args ...any,
) (sql.Result, error) {
	if strings.Contains(
		query,
		"-- name: AdvanceScheduledDeploymentOccurrence",
	) {
		d.once.Do(func() {
			close(d.started)
			select {
			case <-d.resume:
			case <-ctx.Done():
			}
		})
	}
	return d.DB.ExecContext(ctx, query, args...)
}

func (d *scheduleTransitionDB) QueryRowContext(
	ctx context.Context, query string, args ...any,
) *sql.Row {
	if d.fail != "" && strings.Contains(query, "-- name: "+d.fail+" :") {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return d.DB.QueryRowContext(cancelled, query, args...)
	}
	return d.DB.QueryRowContext(ctx, query, args...)
}

func (d *scheduleTransitionDB) QueryContext(
	ctx context.Context, query string, args ...any,
) (*sql.Rows, error) {
	if d.fail != "" && strings.Contains(query, "-- name: "+d.fail+" :") {
		return nil, errors.New("injected schedule read failure")
	}
	return d.DB.QueryContext(ctx, query, args...)
}

type scheduleTransitionFixture struct {
	*artifactE2E
	probe           *scheduleTransitionDB
	row             db.ScheduledDeployment
	release, edited db.Release
	environment     db.Environment
	sched           *scheduler.Scheduler
	logs            *bytes.Buffer
	now             time.Time
}

func newScheduleTransitionFixture(
	t *testing.T, transition string,
) *scheduleTransitionFixture {
	t.Helper()
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "schedule-race@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	probe := &scheduleTransitionDB{
		DB: h.repo.DB, started: make(chan struct{}), resume: make(chan struct{}),
	}
	h.repo.Queries = db.New(probe)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	f := &scheduleTransitionFixture{
		artifactE2E: &artifactE2E{
			h: h, baseURL: srv.URL,
			client: &http.Client{
				Timeout: 10 * time.Second,
				CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
					return http.ErrUseLastResponse
				},
			},
			token: token, session: memberRoleSession(t, h, user.ID),
			csrf:    "member-role-csrf",
			project: seedProject(t, h.repo), environment: seedEnv(t, h.repo),
		},
		probe: probe, logs: &bytes.Buffer{},
	}
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "observed"}, 201), &f.release)
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "edited"}, 201), &f.edited)
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/environments",
		map[string]string{"name": "edited"}, 201), &f.environment)
	if transition == "skip" {
		var lifecycle struct{ ID int64 }
		decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
			map[string]string{"name": "blocked"}, 201), &lifecycle)
		path := fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID)
		f.api(t, "POST", path, map[string]int64{
			"environment_id": f.environment.ID, "sort_order": 1,
		}, 201)
		f.api(t, "POST", path, map[string]int64{
			"environment_id": f.artifactE2E.environment.ID, "sort_order": 2,
		}, 201)
		f.api(t, "PUT", f.base(), map[string]any{
			"name": f.project.Name, "lifecycle_id": lifecycle.ID,
		}, 200)
	}
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/schedules", map[string]any{
		"release_id": f.release.ID, "environment_id": f.artifactE2E.environment.ID,
		"cron": "* * * * *", "enabled": true, "note": "observed",
	}, 201), &f.row)
	if transition == "park" || transition == "unsatisfiable" {
		expression := "invalid"
		if transition == "unsatisfiable" {
			expression = "0 0 31 2 *"
		}
		// Public writes reject invalid cron. Model an existing corrupt row.
		if _, err := h.repo.DB.ExecContext(t.Context(),
			"UPDATE scheduled_deployments SET cron = ? WHERE id = ?",
			expression, f.row.ID); err != nil {
			t.Fatal(err)
		}
	} else if transition == "disable" {
		// Model a legacy snapshot that fails inside occurrence creation.
		if _, err := h.repo.DB.ExecContext(t.Context(),
			"UPDATE releases SET steps_json = ? WHERE id = ?",
			`[{"name":"legacy","script_body":"true"}]`, f.release.ID); err != nil {
			t.Fatal(err)
		}
	}
	f.now = time.Unix(f.row.NextRunAt+1, 0)
	f.sched = scheduler.New(h.repo, h.runner,
		scheduler.WithNow(func() time.Time { return f.now }),
		scheduler.WithLogger(slog.New(slog.NewTextHandler(f.logs, nil))))
	f.sched.SetRunFunc(func(context.Context, int64, int64, int64) {})
	return f
}

func (f *scheduleTransitionFixture) path() string {
	return fmt.Sprintf("%s/schedules/%d", f.base(), f.row.ID)
}

func (f *scheduleTransitionFixture) pause(t *testing.T) func() {
	t.Helper()
	var once sync.Once
	unblock := func() { once.Do(func() { close(f.probe.resume) }) }
	deferCleanup := make(chan struct{})
	t.Cleanup(func() {
		unblock()
		select {
		case <-deferCleanup:
		case <-time.After(10 * time.Second):
			t.Error("scheduler did not stop")
		}
	})
	go func() {
		defer close(deferCleanup)
		f.sched.Tick(t.Context())
	}()
	select {
	case <-f.probe.started:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler did not reach the observed transition")
	}
	return func() {
		unblock()
		select {
		case <-deferCleanup:
		case <-time.After(10 * time.Second):
			t.Fatal("scheduler did not finish")
		}
	}
}

func (f *scheduleTransitionFixture) assertPreserved(
	t *testing.T, edited db.ScheduledDeployment,
) {
	t.Helper()
	var saved db.ScheduledDeployment
	decodeStepLogTest(t, f.api(t, "GET", f.path(), nil, 200), &saved)
	if saved != edited {
		t.Fatalf(
			"stale transition changed edit: saved=%+v edited=%+v",
			saved,
			edited,
		)
	}
	var deployments struct{ Total int }
	decodeStepLogTest(t, f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&deployments)
	if deployments.Total != 0 {
		t.Fatalf("stale occurrence created %d deployments", deployments.Total)
	}
	if strings.Contains(f.logs.String(), "level=ERROR") ||
		strings.Contains(f.logs.String(), "msg=parked") ||
		strings.Contains(f.logs.String(), "disabled invalid") {
		t.Fatalf(
			"stale conflict reported as a failure or transition: %s",
			f.logs,
		)
	}
}

func (f *scheduleTransitionFixture) editSchedule(
	t *testing.T, edit string,
) db.ScheduledDeployment {
	t.Helper()
	var edited db.ScheduledDeployment
	switch edit {
	case "deleted":
		f.api(t, "DELETE", f.path(), nil, 204)
	case "disabled":
		decodeStepLogTest(
			t,
			f.api(t, "POST", f.path()+"/toggle", nil, 200),
			&edited,
		)
	default:
		expression := "* * * * *"
		if edit == "cron" {
			expression = "0 0 1 1 *"
		}
		decodeStepLogTest(
			t,
			f.api(t, "PUT", f.path(), map[string]any{
				"release_id": f.edited.ID, "environment_id": f.environment.ID,
				"cron": expression, "enabled": true, "note": "observed",
			}, 200),
			&edited,
		)
	}
	return edited
}

func (f *scheduleTransitionFixture) assertEditedSchedule(
	t *testing.T, edit string, edited db.ScheduledDeployment,
) {
	t.Helper()
	if edit == "deleted" {
		f.api(t, "GET", f.path(), nil, 404)
	} else {
		f.assertPreserved(t, edited)
		page := f.web(t, "GET",
			fmt.Sprintf("/projects/%d/schedules", f.project.ID), nil, 200)
		if !strings.Contains(page, edited.Cron) {
			t.Fatal("saved schedule is missing from the web list")
		}
		if edited.Enabled == 1 {
			f.api(t, "POST", f.path()+"/toggle", nil, 200)
		}
	}
}

func TestScheduleTransitionsPreserveEditsAPIWebE2E(t *testing.T) {
	for _, transition := range []string{"skip", "park", "unsatisfiable", "disable"} {
		for _, edit := range []string{"cron", "target", "disabled", "deleted"} {
			t.Run(transition+"/"+edit, func(t *testing.T) {
				f := newScheduleTransitionFixture(t, transition)
				finish := f.pause(t)
				edited := f.editSchedule(t, edit)
				finish()
				f.assertEditedSchedule(t, edit, edited)
			})
		}
	}
}

func TestScheduleTransientReadsRetainDueOccurrenceE2E(t *testing.T) {
	for _, query := range []string{"GetProject", "GetRelease", "ListLifecycleStages"} {
		t.Run(query, func(t *testing.T) {
			f := newScheduleTransitionFixture(t, "skip")
			f.probe.fail = query
			f.sched.Tick(t.Context())
			f.probe.fail = ""
			var saved db.ScheduledDeployment
			decodeStepLogTest(t, f.api(t, "GET", f.path(), nil, 200), &saved)
			if saved.NextRunAt != f.row.NextRunAt || saved.LastFiredAt.Valid ||
				saved.Enabled != 1 || !strings.Contains(f.logs.String(), "level=ERROR") {
				t.Fatalf(
					"transient failure consumed occurrence: %+v logs=%s",
					saved,
					f.logs,
				)
			}
			// Once storage recovers, an intentional gate skip advances normally.
			close(f.probe.resume)
			f.sched.Tick(t.Context())
			decodeStepLogTest(t, f.api(t, "GET", f.path(), nil, 200), &saved)
			if saved.NextRunAt <= f.now.Unix() || saved.LastFiredAt.Valid {
				t.Fatalf("gate skip did not advance without firing: %+v", saved)
			}
			f.api(t, "POST", f.path()+"/toggle", nil, 200)
		})
	}
}
