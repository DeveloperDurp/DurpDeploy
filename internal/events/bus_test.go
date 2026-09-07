package events_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/notify"
	"durpdeploy/internal/repository"
)

type fakeNotifier struct {
	name    string
	skip    bool
	failErr error
	calls   []events.Event
}

func (f *fakeNotifier) Name() string { return f.name }

func (f *fakeNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	f.calls = append(f.calls, event)
	if f.skip {
		return true, nil
	}
	if f.failErr != nil {
		return false, f.failErr
	}
	return false, nil
}

func newTestRepo(t *testing.T) *repository.Repository {
	t.Helper()
	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })
	return repository.New(dbConn)
}

// TestBus_PublishRecordsResultsAndFillsSettings: Publish loads the
// project's slack_webhook_url/notify_emails onto the event before invoking
// notifiers, invokes every registered notifier, and persists a
// notification_events row whose results JSON has one entry per notifier.
func TestBus_PublishRecordsResultsAndFillsSettings(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	proj, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p1"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := repo.Queries.UpdateProjectNotifications(
		ctx,
		db.UpdateProjectNotificationsParams{
			SlackWebhookUrl: sql.NullString{
				String: "https://hooks.slack.example/x",
				Valid:  true,
			},
			NotifyEmails: sql.NullString{
				String: "a@example.com, b@example.com",
				Valid:  true,
			},
			ID: proj.ID,
		},
	); err != nil {
		t.Fatalf("update notifications: %v", err)
	}

	ok := &fakeNotifier{name: "slack"}
	skip := &fakeNotifier{name: "email", skip: true}

	bus := events.NewBus(repo)
	bus.Register(ok)
	bus.Register(skip)

	bus.Publish(ctx, events.Event{
		Type:      events.DeploymentStarted,
		ProjectID: proj.ID,
		Message:   "started",
	})

	if len(ok.calls) != 1 {
		t.Fatalf("slack notifier calls = %d, want 1", len(ok.calls))
	}
	got := ok.calls[0]
	if got.SlackWebhookURL != "https://hooks.slack.example/x" {
		t.Fatalf("webhook url = %q", got.SlackWebhookURL)
	}
	if len(got.NotifyEmails) != 2 || got.NotifyEmails[0] != "a@example.com" ||
		got.NotifyEmails[1] != "b@example.com" {
		t.Fatalf("notify emails = %v", got.NotifyEmails)
	}

	rows, err := repo.Queries.ListNotificationEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("event rows = %d, want 1", len(rows))
	}
	var results map[string]string
	if err := json.Unmarshal([]byte(rows[0].Results), &results); err != nil {
		t.Fatalf("unmarshal results: %v", err)
	}
	if results["slack"] != "ok" {
		t.Fatalf("slack result = %q, want ok", results["slack"])
	}
	if results["email"] != "skipped" {
		t.Fatalf("email result = %q, want skipped", results["email"])
	}
}

// TestBus_PublishRecordsFailure: a notifier returning an error is recorded
// as "failed: <err>" rather than aborting the publish or the other notifiers.
func TestBus_PublishRecordsFailure(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	proj, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p1"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	failing := &fakeNotifier{name: "slack", failErr: context.DeadlineExceeded}

	bus := events.NewBus(repo)
	bus.Register(failing)
	bus.Publish(ctx, events.Event{ProjectID: proj.ID, Message: "x"})

	rows, err := repo.Queries.ListNotificationEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("event rows = %d, want 1", len(rows))
	}
	var results map[string]string
	if err := json.Unmarshal([]byte(rows[0].Results), &results); err != nil {
		t.Fatalf("unmarshal results: %v", err)
	}
	if results["slack"] == "" || results["slack"] == "ok" {
		t.Fatalf(
			"slack result = %q, want a failed: ... status",
			results["slack"],
		)
	}
}

func TestBus_PublishDeliversLocalDeploymentToWebhook(t *testing.T) {
	// Given
	repo := newTestRepo(t)
	ctx := context.Background()
	var receipts int
	receiver := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		receipts++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "local-project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := repo.Queries.UpdateProjectNotifications(
		ctx,
		db.UpdateProjectNotificationsParams{
			SlackWebhookUrl: sql.NullString{String: receiver.URL, Valid: true},
			ID:              project.ID,
		},
	); err != nil {
		t.Fatalf("configure webhook: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "local-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	bus := events.NewBus(repo)
	bus.Register(notify.NewSlackNotifierWithClient(receiver.Client()))

	// When
	bus.Publish(ctx, events.Event{
		Type:          events.DeploymentStarted,
		DeploymentID:  deployment.ID,
		ProjectID:     project.ID,
		EnvironmentID: environment.ID,
		Message:       "local deployment started",
	})

	// Then
	if receipts != 1 {
		t.Fatalf("webhook receipts = %d, want 1", receipts)
	}
	var notifications int
	if err := repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM notification_events WHERE deployment_id = ?",
		deployment.ID,
	).Scan(&notifications); err != nil {
		t.Fatalf("count notification events: %v", err)
	}
	if notifications != 1 {
		t.Fatalf("notification events = %d, want 1", notifications)
	}
}
