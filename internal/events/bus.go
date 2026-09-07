// Package events implements a small in-memory pub/sub bus that decouples
// the deployment runner from notification delivery (Slack, email, Gotify,
// Discord, ...). The runner publishes lifecycle events; the bus fans each
// one out to every registered Notifier and persists the outcome to
// notification_events so admins can observe deliveries via
// /admin/notifications.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// Type identifies the kind of deployment lifecycle event being published.
type Type string

const (
	DeploymentStarted   Type = "deployment_started"
	DeploymentSucceeded Type = "deployment_succeeded"
	DeploymentFailed    Type = "deployment_failed"
	DeploymentCancelled Type = "deployment_cancelled"
	BackupUnhealthy     Type = "backup_unhealthy"
	BackupHealthy       Type = "backup_healthy"
)

// Event carries everything a Notifier needs to describe and deliver a
// deployment status change. SlackWebhookURL/NotifyEmails are filled in by
// Bus.Publish from the project's notification settings, not by the caller.
type Event struct {
	Type              Type
	DeploymentID      int64
	ProjectID         int64
	EnvironmentID     int64
	Message           string
	SlackWebhookURL   string
	NotifyEmails      []string
	GotifyURL         string
	GotifyToken       string
	DiscordWebhookURL string
}

// Notifier delivers an Event through one channel (Slack, email, ...).
// Skipped=true means the notifier isn't configured for this event (e.g. no
// webhook URL set) and is recorded as "skipped" rather than "failed".
type Notifier interface {
	Name() string
	Notify(ctx context.Context, event Event) (skipped bool, err error)
}

// Bus fans out published events to every registered Notifier and logs the
// result of each to notification_events.
//
// ponytail: notifiers run synchronously, one at a time, on the publishing
// goroutine (already the runner's own background goroutine, not an HTTP
// request). A worker pool / async queue is the upgrade path if delivery
// latency ever needs to stop blocking the runner between steps.
type Bus struct {
	repo      *repository.Repository
	notifiers []Notifier
	mu        sync.Mutex
}

func NewBus(repo *repository.Repository) *Bus {
	return &Bus{repo: repo}
}

// Register adds a Notifier that every future Publish call will invoke.
func (b *Bus) Register(n Notifier) {
	b.notifiers = append(b.notifiers, n)
}

// splitEmails parses a comma-separated notify_emails column into a
// trimmed, non-empty slice. Shared between the project and global
// notification-settings lookups in Publish.
func splitEmails(v sql.NullString) []string {
	if !v.Valid || v.String == "" {
		return nil
	}
	var emails []string
	for _, p := range strings.Split(v.String, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			emails = append(emails, p)
		}
	}
	return emails
}

// Publish loads the target project's notification settings, runs every
// registered notifier, and records the event plus each notifier's outcome.
// Errors loading the project or writing the history row are swallowed
// (best-effort observability; must never fail the deployment itself).
func (b *Bus) Publish(ctx context.Context, evt Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publish(ctx, evt)
}

func (b *Bus) publish(ctx context.Context, evt Event) {
	var notify bool
	evt, notify = b.rootDeploymentEvent(ctx, evt)
	if !notify || b.notificationExists(ctx, evt) {
		return
	}
	if isTerminalDeploymentEvent(evt.Type) &&
		b.deploymentStarted(ctx, evt.DeploymentID) {
		b.publish(ctx, Event{
			Type:          DeploymentStarted,
			DeploymentID:  evt.DeploymentID,
			ProjectID:     evt.ProjectID,
			EnvironmentID: evt.EnvironmentID,
			Message:       "Deployment started",
		})
	}
	b.deliver(ctx, evt)
}

func (b *Bus) deliver(ctx context.Context, evt Event) {
	if evt.ProjectID == 0 {
		// Project-less/system-wide event (e.g. backup health): load
		// channels from the global_notifications singleton instead of a
		// project row.
		if global, err := b.repo.Queries.GetGlobalNotifications(
			ctx,
		); err == nil {
			evt.SlackWebhookURL = global.SlackWebhookUrl.String
			evt.NotifyEmails = splitEmails(global.NotifyEmails)
			evt.GotifyURL = global.GotifyUrl.String
			evt.GotifyToken = global.GotifyToken.String
			evt.DiscordWebhookURL = global.DiscordWebhookUrl.String
		}
	} else if project, err := b.repo.Queries.GetProject(
		ctx,
		evt.ProjectID,
	); err == nil {
		evt.SlackWebhookURL = project.SlackWebhookUrl.String
		evt.NotifyEmails = splitEmails(project.NotifyEmails)
		evt.GotifyURL = project.GotifyUrl.String
		evt.GotifyToken = project.GotifyToken.String
		evt.DiscordWebhookURL = project.DiscordWebhookUrl.String
	}

	results := make(map[string]string, len(b.notifiers))
	for _, n := range b.notifiers {
		skipped, err := n.Notify(ctx, evt)
		switch {
		case skipped:
			results[n.Name()] = "skipped"
		case err != nil:
			results[n.Name()] = "failed: " + err.Error()
		default:
			results[n.Name()] = "ok"
		}
	}
	resultsJSON, _ := json.Marshal(results)

	// FK columns (deployment_id/project_id/environment_id) must only be
	// marked Valid when an id was actually supplied — otherwise a zero
	// value would reference a nonexistent row 0 and fail the FK
	// constraint, silently dropping the whole history row.
	_, _ = b.repo.Queries.CreateNotificationEvent(
		ctx,
		db.CreateNotificationEventParams{
			EventType: string(evt.Type),
			DeploymentID: sql.NullInt64{
				Int64: evt.DeploymentID,
				Valid: evt.DeploymentID != 0,
			},
			ProjectID: sql.NullInt64{
				Int64: evt.ProjectID,
				Valid: evt.ProjectID != 0,
			},
			EnvironmentID: sql.NullInt64{
				Int64: evt.EnvironmentID,
				Valid: evt.EnvironmentID != 0,
			},
			Message: evt.Message,
			Results: string(resultsJSON),
		},
	)
}

// rootDeploymentEvent maps child lifecycle events after parent aggregation.
func (b *Bus) rootDeploymentEvent(
	ctx context.Context,
	evt Event,
) (Event, bool) {
	if !isDeploymentEvent(evt.Type) || evt.DeploymentID == 0 {
		return evt, true
	}
	deployment, err := b.repo.Queries.GetDeployment(ctx, evt.DeploymentID)
	if err != nil || !deployment.ParentDeploymentID.Valid {
		return evt, true
	}
	root, err := b.repo.Queries.GetDeployment(
		ctx,
		deployment.ParentDeploymentID.Int64,
	)
	if err != nil {
		return evt, false
	}
	switch evt.Type {
	case DeploymentStarted:
		if !root.StartedAt.Valid {
			return evt, false
		}
	case DeploymentSucceeded, DeploymentFailed, DeploymentCancelled:
		terminalType, ok := terminalDeploymentEvent(root.Status)
		if !ok {
			return evt, false
		}
		evt.Type = terminalType
	}
	evt.DeploymentID = root.ID
	evt.EnvironmentID = root.EnvironmentID
	return evt, true
}

func isDeploymentEvent(typ Type) bool {
	switch typ {
	case DeploymentStarted, DeploymentSucceeded, DeploymentFailed,
		DeploymentCancelled:
		return true
	default:
		return false
	}
}

func isTerminalDeploymentEvent(typ Type) bool {
	return typ == DeploymentSucceeded || typ == DeploymentFailed ||
		typ == DeploymentCancelled
}

func (b *Bus) deploymentStarted(ctx context.Context, deploymentID int64) bool {
	if deploymentID == 0 {
		return false
	}
	deployment, err := b.repo.Queries.GetDeployment(ctx, deploymentID)
	return err == nil && deployment.StartedAt.Valid
}

func terminalDeploymentEvent(status string) (Type, bool) {
	switch status {
	case "succeeded":
		return DeploymentSucceeded, true
	case "failed":
		return DeploymentFailed, true
	case "cancelled":
		return DeploymentCancelled, true
	default:
		return "", false
	}
}

func (b *Bus) notificationExists(ctx context.Context, evt Event) bool {
	if !isDeploymentEvent(evt.Type) || evt.DeploymentID == 0 {
		return false
	}
	var count int
	if err := b.repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM notification_events WHERE event_type = ? AND deployment_id = ?",
		string(evt.Type),
		evt.DeploymentID,
	).Scan(&count); err != nil {
		return false
	}
	return count != 0
}
