package events_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestBus_DelayedChildStartNotifiesStartedRootBeforeTerminal(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	var mu sync.Mutex
	var receipts int
	receiver := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		mu.Lock()
		receipts++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	if _, err := repo.DB.ExecContext(ctx, `
		INSERT INTO projects(id, name, slack_webhook_url) VALUES(1, 'gate', ?);
		INSERT INTO environments(id, name) VALUES(1, 'gate');
		INSERT INTO releases(id, project_id, version, steps_json) VALUES(1, 1, 'v1', '[]');
		INSERT INTO agents(id, name, status) VALUES('a', 'Agent', 'pending');
		INSERT INTO deployments(id, release_id, environment_id, status, started_at) VALUES(1, 1, 1, 'running', 10);
		INSERT INTO deployments(id, release_id, environment_id, status, started_at, parent_deployment_id, target_agent_id, target_agent_name) VALUES(2, 1, 1, 'running', 10, 1, 'a', 'Agent');
		INSERT INTO deployments(id, release_id, environment_id, status) VALUES(3, 1, 1, 'pending');
		INSERT INTO deployments(id, release_id, environment_id, status, parent_deployment_id, target_agent_id, target_agent_name) VALUES(4, 1, 1, 'pending', 3, 'a', 'Agent');
	`, receiver.URL); err != nil {
		t.Fatalf("seed delayed start: %v", err)
	}
	bus := events.NewBus(repo)
	bus.Register(notify.NewSlackNotifierWithClient(receiver.Client()))
	if _, err := repo.DB.ExecContext(ctx, `
		UPDATE deployments SET status = 'cancelled', finished_at = 20 WHERE id IN (1, 2, 3, 4)
	`); err != nil {
		t.Fatalf("settle deployments before delayed callbacks: %v", err)
	}

	var calls sync.WaitGroup
	calls.Add(2)
	go func() {
		defer calls.Done()
		bus.Publish(ctx, events.Event{
			Type: events.DeploymentStarted, DeploymentID: 2,
			ProjectID: 1, EnvironmentID: 1, Message: "delayed start",
		})
	}()
	go func() {
		defer calls.Done()
		bus.Publish(ctx, events.Event{
			Type: events.DeploymentCancelled, DeploymentID: 2,
			ProjectID: 1, EnvironmentID: 1, Message: "terminal",
		})
	}()
	calls.Wait()
	bus.Publish(ctx, events.Event{
		Type: events.DeploymentCancelled, DeploymentID: 4,
		ProjectID: 1, EnvironmentID: 1, Message: "queued terminal",
	})

	var rootStarts, rootTerminals, childNotifications, queuedStarts int
	if err := repo.DB.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE deployment_id = 1 AND event_type = 'deployment_started'),
			COUNT(*) FILTER (WHERE deployment_id = 1 AND event_type = 'deployment_cancelled'),
			COUNT(*) FILTER (WHERE deployment_id IN (2, 4)),
			COUNT(*) FILTER (WHERE deployment_id = 3 AND event_type = 'deployment_started')
		FROM notification_events
	`).Scan(&rootStarts, &rootTerminals, &childNotifications, &queuedStarts); err != nil {
		t.Fatalf("count delayed notification events: %v", err)
	}
	mu.Lock()
	gotReceipts := receipts
	mu.Unlock()
	if rootStarts != 1 || rootTerminals != 1 || childNotifications != 0 ||
		queuedStarts != 0 || gotReceipts != 3 {
		t.Fatalf(
			"delayed root start/terminal/child/queued/receipts = %d/%d/%d/%d/%d, want 1/1/0/0/3",
			rootStarts,
			rootTerminals,
			childNotifications,
			queuedStarts,
			gotReceipts,
		)
	}
	rows, err := repo.DB.QueryContext(ctx, `
		SELECT event_type FROM notification_events
		WHERE deployment_id = 1 ORDER BY id
	`)
	if err != nil {
		t.Fatalf("list delayed root event order: %v", err)
	}
	defer rows.Close()
	var rootTypes []string
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("scan delayed root event: %v", err)
		}
		rootTypes = append(rootTypes, eventType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate delayed root events: %v", err)
	}
	if len(rootTypes) != 2 || rootTypes[0] != "deployment_started" ||
		rootTypes[1] != "deployment_cancelled" {
		t.Fatalf(
			"delayed root event order = %v, want start then cancelled",
			rootTypes,
		)
	}
	t.Logf(
		"receiver receipts=%d delayed_root_start=%d terminal=%d child=%d queued_start=%d",
		gotReceipts,
		rootStarts,
		rootTerminals,
		childNotifications,
		queuedStarts,
	)
}
