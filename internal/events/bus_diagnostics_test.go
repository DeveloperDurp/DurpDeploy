package events_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

type notificationFault struct {
	db.DBTX
	query string
}

func (f notificationFault) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if strings.Contains(query, "-- name: "+f.query+" ") {
		// A driver error can contain credentials. It must never reach the log.
		return f.DBTX.QueryRowContext(
			ctx,
			"SELECT * FROM credential_secret_driver_detail",
		)
	}
	return f.DBTX.QueryRowContext(ctx, query, args...)
}

func TestBus_SettingsAndHistoryDiagnostics(t *testing.T) {
	for _, scenario := range []string{"project", "global", "history", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			checkNotificationDiagnostic(t, scenario)
		})
	}
}

func checkNotificationDiagnostic(t *testing.T, scenario string) {
	t.Helper()
	repo := newTestRepo(t)
	evt := seedNotificationDiagnostic(t, repo.Queries)
	query := "GetProject"
	if scenario == "global" {
		query = "GetGlobalNotifications"
		evt.ProjectID = 0
		evt.DeploymentID = 0
		evt.EnvironmentID = 0
		evt.Type = events.BackupHealthy
	}
	if scenario == "history" {
		query = "CreateNotificationEvent"
	}
	if scenario != "disabled" {
		repo.Queries = db.New(
			notificationFault{DBTX: repo.DB, query: query},
		)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	n := &fakeNotifier{name: "slack", skip: scenario == "disabled"}
	bus := events.NewBus(repo)
	bus.Register(n)
	bus.Register(&fakeNotifier{name: "email", skip: true})
	bus.Publish(t.Context(), evt)
	if strings.Contains(logs.String(), "credential_secret") {
		t.Fatal("diagnostics exposed credential-bearing details")
	}
	if scenario == "disabled" {
		if logs.Len() != 0 || len(n.calls) != 1 {
			t.Fatalf(
				"disabled channel diagnostics/calls: %s %d",
				logs.String(),
				len(n.calls),
			)
		}
	} else {
		assertNotificationDiagnostic(t, scenario, evt, &logs, n)
	}
	assertNotificationDiagnosticHistory(t, scenario, repo.Queries)
	// Removing the isolated query fault restores normal delivery/history.
	repo.Queries = db.New(repo.DB)
	bus.Publish(t.Context(), evt)
	rows, err := repo.Queries.ListNotificationEvents(t.Context(), 10)
	if err != nil || len(rows) == 0 || len(n.calls) == 0 {
		t.Fatalf(
			"recovery: rows=%d calls=%d error=%v",
			len(rows),
			len(n.calls),
			err,
		)
	}
}

func seedNotificationDiagnostic(
	t *testing.T,
	queries *db.Queries,
) events.Event {
	t.Helper()
	project, err := queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "diagnostics"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "diagnostics"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "succeeded",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	evt := events.Event{
		Type:            events.DeploymentSucceeded,
		DeploymentID:    deployment.ID,
		ProjectID:       project.ID,
		EnvironmentID:   env.ID,
		Message:         "credential_secret_message",
		SlackWebhookURL: "https://credential_secret_webhook",
		GotifyToken:     "credential_secret_token",
		NotifyEmails: []string{
			"credential_secret_recipient@example.test",
		},
	}
	return evt
}

func assertNotificationDiagnostic(
	t *testing.T,
	scenario string,
	evt events.Event,
	logs *bytes.Buffer,
	n *fakeNotifier,
) {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	want := "load notification settings failed"
	if scenario == "history" {
		want = "write notification history failed"
	}
	if entry["msg"] != want || entry["level"] != "WARN" ||
		entry["event_type"] != string(evt.Type) ||
		entry["deployment_id"] != float64(evt.DeploymentID) ||
		entry["project_id"] != float64(evt.ProjectID) ||
		entry["environment_id"] != float64(evt.EnvironmentID) {
		t.Fatalf("diagnostic lacks safe context: %s", logs.String())
	}
	if scenario != "history" {
		if entry["settings_scope"] != scenario || len(n.calls) != 0 {
			t.Fatalf(
				"unavailable settings invoked delivery: %s calls=%d",
				logs.String(),
				len(n.calls),
			)
		}
	} else if len(n.calls) != 1 {
		t.Fatal("history failure prevented delivery")
	}
}

func assertNotificationDiagnosticHistory(
	t *testing.T,
	scenario string,
	queries *db.Queries,
) {
	t.Helper()
	rows, err := queries.ListNotificationEvents(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if scenario == "history" {
		if len(rows) != 0 {
			t.Fatal("failed history insert created a row")
		}
	} else {
		if len(rows) != 1 {
			t.Fatalf("history rows=%d", len(rows))
		}
		var results map[string]string
		if err := json.Unmarshal([]byte(rows[0].Results), &results); err != nil {
			t.Fatal(err)
		}
		want := "failed: delivery failed"
		if scenario == "disabled" {
			want = "skipped"
		}
		if results["slack"] != want || results["email"] != want {
			t.Fatalf("channel classification: %v", results)
		}
	}
}
