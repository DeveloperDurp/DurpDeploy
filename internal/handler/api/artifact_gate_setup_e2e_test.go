//go:build e2e

package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/scheduler"
	"durpdeploy/views/pages"
)

type failedGateLookupDB struct {
	*sql.DB
	failed      atomic.Bool
	failRelease bool
}

func (d *failedGateLookupDB) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if d.failRelease && d.failed.Load() &&
		strings.Contains(query, "-- name: GetRelease :") {
		failed, cancel := context.WithCancel(ctx)
		cancel()
		return d.DB.QueryRowContext(failed, query, args...)
	}
	if strings.Contains(query, "-- name: GetArtifactGateRun") &&
		!d.failed.Swap(true) {
		failed, cancel := context.WithCancel(ctx)
		cancel()
		return d.DB.QueryRowContext(failed, query, args...)
	}
	return d.DB.QueryRowContext(ctx, query, args...)
}

func TestArtifactGateFailureDoesNotUseGlobalNotifiersE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release := verificationRelease(t, f, "release-lookup-failure")
	deployment, err := f.h.repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: f.environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.DB.Exec(
		"UPDATE global_notifications SET gotify_url='https://global.invalid'",
	); err != nil {
		t.Fatal(err)
	}
	f.h.repo.Queries = db.New(
		&failedGateLookupDB{DB: f.h.repo.DB, failRelease: true},
	)
	f.h.runner.Run(t.Context(), deployment.ID, release.ID, f.environment.ID)
	var stored db.Deployment
	if err := json.Unmarshal(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
			nil,
			200,
		),
		&stored,
	); err != nil ||
		stored.Status != "failed" {
		t.Fatalf("unresolved project failure=%+v err=%v", stored, err)
	}
	var count int
	if err := f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM notification_events WHERE deployment_id=?", deployment.ID).
		Scan(&count); err != nil ||
		count != 0 ||
		len(f.done) != 0 {
		t.Fatalf(
			"unresolved project reached notifiers: count=%d err=%v",
			count,
			err,
		)
	}
}

func TestArtifactGateSetupFailureNotifiesE2E(t *testing.T) {
	for _, scenario := range []string{"ordinary-claim", "gated-claim", "capture"} {
		t.Run(scenario, func(t *testing.T) {
			f := newArtifactE2E(t)
			step := map[string]any{
				"name":            "Generate",
				"container_image": "docker.io/library/bash:5.2",
				"script_body":     "true",
			}
			if scenario != "ordinary-claim" {
				step["approval_artifact_path"] = "plan"
				step["approval_review_path"] = "review"
				step["approval_review_format"] = "summary"
				step["script_body"] = `echo sensitive-generation-output; printf '{"create":1}' > "$DURPDEPLOY_STAGE_DIR/review"`
			}
			f.api(t, "POST", f.base()+"/steps", step, 201)
			f.api(
				t,
				"POST",
				f.base()+"/steps",
				map[string]any{
					"name":            "Apply",
					"container_image": "docker.io/library/bash:5.2",
					"script_body":     "true",
				},
				201,
			)
			release := verificationRelease(t, f, scenario)
			if scenario != "capture" {
				f.h.repo.Queries = db.New(&failedGateLookupDB{DB: f.h.repo.DB})
			}
			deployment := verificationDeploy(t, f, release)
			f.completion(t, deployment.ID, events.DeploymentFailed)
			var stored db.Deployment
			if err := json.Unmarshal(
				f.api(
					t,
					"GET",
					fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
					nil,
					200,
				),
				&stored,
			); err != nil ||
				stored.Status != "failed" {
				t.Fatalf(
					"setup failure stranded deployment: %+v err=%v",
					stored,
					err,
				)
			}
			var count int
			deadline := time.Now().Add(5 * time.Second)
			for {
				err := f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM notification_events WHERE deployment_id=? AND event_type='deployment_failed' AND message='Deployment failed'", deployment.ID).
					Scan(&count)
				if err != nil {
					t.Fatal(err)
				}
				if count == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("generic terminal notifications=%d", count)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestArtifactGatePublishingCancellationE2E(t *testing.T) {
	for _, surface := range []string{"api", "web"} {
		t.Run(surface, func(t *testing.T) {
			f, deployment, _ := newGateDeployment(t)
			if _, err := f.h.repo.DB.Exec(
				"UPDATE deployments SET status='publishing_artifact' WHERE id=?",
				deployment.ID,
			); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.h.runner.RegisterCancel(deployment.ID, cancel)
			if surface == "api" {
				f.api(
					t,
					"POST",
					fmt.Sprintf("/api/v1/deployments/%d/cancel", deployment.ID),
					nil,
					200,
				)
			} else {
				f.web(
					t,
					"POST",
					fmt.Sprintf("/deployments/%d/cancel", deployment.ID),
					url.Values{},
					303,
				)
			}
			if ctx.Err() == nil {
				t.Fatal("publishing cancellation did not reach the runner")
			}
			if _, err := f.h.repo.Queries.CancelStepDeployment(
				t.Context(),
				deployment.ID,
			); err != nil {
				t.Fatal(err)
			}
			var gates []pages.ArtifactGateInfo
			if err := json.Unmarshal(
				f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
				&gates,
			); err != nil || len(gates) != 1 ||
				gates[0].Status != "cancelled" {
				t.Fatalf("terminal gate=%+v err=%v", gates, err)
			}
		})
	}
}

func TestArtifactGateInvalidScheduleDisabledE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(
		t,
		"POST",
		f.base()+"/steps",
		map[string]any{
			"name":                   "Final gate",
			"container_image":        "docker.io/library/bash:5.2",
			"script_body":            "true",
			"approval_artifact_path": "plan",
			"approval_review_path":   "review",
			"approval_review_format": "summary",
		},
		201,
	)
	release := verificationRelease(t, f, "invalid-final-gate")
	var schedule db.ScheduledDeployment
	if err := json.Unmarshal(
		f.api(
			t,
			"POST",
			f.base()+"/schedules",
			map[string]any{
				"release_id":     release.ID,
				"environment_id": f.environment.ID,
				"cron":           "* * * * *",
				"enabled":        true,
			},
			201,
		),
		&schedule,
	); err != nil {
		t.Fatal(err)
	}
	scheduler.New(f.h.repo, f.h.runner, scheduler.WithNow(func() time.Time { return time.Now().Add(2 * time.Minute) })).
		Tick(t.Context())
	if err := json.Unmarshal(
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/schedules/%d", f.base(), schedule.ID),
			nil,
			200,
		),
		&schedule,
	); err != nil || schedule.Enabled != 0 ||
		!strings.Contains(schedule.LastError, "artifact approval") {
		t.Fatalf("invalid gate schedule=%+v err=%v", schedule, err)
	}
}
