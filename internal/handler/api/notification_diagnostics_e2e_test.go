//go:build e2e

package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

type notificationQueryFault struct {
	db.DBTX
	query   string
	enabled *atomic.Bool
}

func (f notificationQueryFault) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if f.enabled.Load() && strings.Contains(query, "-- name: "+f.query+" ") {
		return f.DBTX.QueryRowContext(
			ctx,
			"SELECT * FROM credential_secret_driver_detail",
		)
	}
	return f.DBTX.QueryRowContext(ctx, query, args...)
}

type notificationDiagnostics chan map[string]any

func (logs notificationDiagnostics) Write(data []byte) (int, error) {
	var record map[string]any
	if json.Unmarshal(data, &record) == nil &&
		(record["msg"] == "load notification settings failed" || record["msg"] == "write notification history failed") {
		select {
		case logs <- record:
		default:
		}
	}
	return len(data), nil
}

type notificationProbe struct{ calls *atomic.Int64 }

func (n notificationProbe) Name() string { return "notification-probe" }
func (n notificationProbe) Notify(context.Context, events.Event) (bool, error) {
	n.calls.Add(1)
	return true, nil
}

func TestNotificationDiagnosticsAPIWebE2E(t *testing.T) {
	for _, scope := range []string{"project", "global", "history"} {
		for _, status := range []string{"succeeded", "failed"} {
			t.Run(scope+"/"+status, func(t *testing.T) {
				checkNotificationDiagnosticsAPIWebCase(t, scope, status)
			})
		}
	}
}

func checkNotificationDiagnosticsAPIWebCase(
	t *testing.T,
	scope, status string,
) {
	t.Helper()
	f := newArtifactE2E(t)
	logs := make(notificationDiagnostics, 16)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	query := "GetProject"
	if scope == "global" {
		query = "GetGlobalNotifications"
	}
	if scope == "history" {
		query = "CreateNotificationEvent"
	}
	var enabled atomic.Bool
	enabled.Store(true)
	busRepo := *f.h.repo
	busRepo.Queries = db.New(
		notificationQueryFault{
			DBTX:    f.h.repo.DB,
			query:   query,
			enabled: &enabled,
		},
	)
	bus := events.NewBus(&busRepo)
	var calls atomic.Int64
	bus.Register(notificationProbe{&calls})
	f.h.runner.SetEventBus(bus)
	if scope == "global" {
		bus.Publish(
			t.Context(),
			events.Event{
				Type:    events.BackupUnhealthy,
				Message: "system notification",
			},
		)
	}
	deployment := runNotificationDiagnosticDeployment(t, f, status)
	checkNotificationAPIDiagnostic(t, f, scope, logs, deployment, &calls)
	body := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(body, status) {
		t.Fatal("web deployment result changed")
	}
	checkNotificationAPIRecovery(t, f, &enabled, bus)
}

func runNotificationDiagnosticDeployment(
	t *testing.T,
	f *artifactE2E,
	status string,
) db.Deployment {
	t.Helper()
	script := "echo execution-ok"
	if status == "failed" {
		script = "echo execution-failed; exit 7"
	}
	f.api(
		t,
		"POST",
		f.base()+"/steps",
		map[string]string{
			"name":            "notification diagnostics",
			"script_body":     script,
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		},
		201,
	)
	var release db.Release
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": "diagnostics"},
			201,
		),
		&release,
	)
	var deployment db.Deployment
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": f.environment.ID,
			},
			201,
		),
		&deployment,
	)
	waitVerificationStatus(t, f, deployment.ID, status)
	return deployment
}

func checkNotificationAPIDiagnostic(
	t *testing.T,
	f *artifactE2E,
	scope string,
	logs notificationDiagnostics,
	deployment db.Deployment,
	calls *atomic.Int64,
) {
	t.Helper()
	var diagnostic map[string]any
	select {
	case diagnostic = <-logs:
	case <-time.After(5 * time.Second):
		t.Fatal("missing notification diagnostic")
	}
	encoded, err := json.Marshal(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "credential_secret") {
		t.Fatal("driver details reached diagnostics")
	}
	if scope != "global" &&
		(diagnostic["deployment_id"] != float64(deployment.ID) || diagnostic["project_id"] != float64(f.project.ID) || diagnostic["environment_id"] != float64(f.environment.ID)) {
		t.Fatalf("wrong entity IDs: %s", encoded)
	}
	checkNotificationAPIProcessing(t, f, scope, diagnostic, encoded, calls)
}

func checkNotificationAPIProcessing(
	t *testing.T,
	f *artifactE2E,
	scope string,
	diagnostic map[string]any,
	encoded []byte,
	calls *atomic.Int64,
) {
	t.Helper()
	if scope == "history" {
		if diagnostic["msg"] != "write notification history failed" ||
			calls.Load() == 0 {
			t.Fatal(
				"history failure stopped notification processing",
			)
		}
		var rows []db.NotificationEvent
		decodeStepLogTest(
			t,
			f.api(
				t,
				"GET",
				"/api/v1/admin/notifications",
				nil,
				200,
			),
			&rows,
		)
		if len(rows) != 0 {
			t.Fatal("failed history insert created rows")
		}
	} else {
		if diagnostic["settings_scope"] != scope {
			t.Fatalf("wrong settings scope: %s", encoded)
		}
		awaitNotificationHistory(t, f, "failed: delivery failed")
		if !strings.Contains(f.web(t, "GET", "/admin/notifications", nil, 200), "failed: delivery failed") {
			t.Fatal("web history omitted lookup failure")
		}
		if scope == "project" && calls.Load() != 0 {
			t.Fatal("unavailable project settings invoked delivery")
		}
	}
}

func checkNotificationAPIRecovery(
	t *testing.T,
	f *artifactE2E,
	enabled *atomic.Bool,
	bus *events.Bus,
) {
	t.Helper()
	// Finish any terminal Publish before changing the fault for recovery.
	f.h.runner.KillAll()
	enabled.Store(false)
	bus.Publish(
		t.Context(),
		events.Event{
			Type:    events.BackupHealthy,
			Message: "diagnostic recovery",
		},
	)
	awaitNotificationHistory(t, f, "diagnostic recovery")
	for _, body := range []string{string(f.api(t, "GET", "/api/v1/admin/notifications", nil, 200)), f.web(t, "GET", "/admin/notifications", nil, 200)} {
		if !strings.Contains(body, "skipped") ||
			strings.Contains(body, "credential_secret") {
			t.Fatal("history contract or redaction changed")
		}
	}
}
