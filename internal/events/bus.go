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
	"fmt"
	"log/slog"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/provenance"
	"durpdeploy/internal/repository"
)

// Type identifies the kind of deployment lifecycle event being published.
type Type string

const (
	ArtifactAwaitingApproval Type = "artifact_awaiting_approval"
	DeploymentStarted        Type = "deployment_started"
	DeploymentSucceeded      Type = "deployment_succeeded"
	DeploymentFailed         Type = "deployment_failed"
	RunbookStarted           Type = "runbook_started"
	RunbookSucceeded         Type = "runbook_succeeded"
	RunbookFailed            Type = "runbook_failed"
	BackupUnhealthy          Type = "backup_unhealthy"
	BackupHealthy            Type = "backup_healthy"
	AgentStale               Type = "agent_stale"
	AgentOffline             Type = "agent_offline"
	AgentRecovered           Type = "agent_recovered"
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
// An exact SMTPPlaintextWarning means email delivery succeeded without TLS.
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

// Publish loads notification settings, delivers when they are available,
// and records the event plus each channel's outcome.
// Settings and history failures produce diagnostics without provider or driver
// details. Delivery remains best effort and never changes deployment outcomes.
func (b *Bus) Publish(ctx context.Context, evt Event) {
	if evt.DeploymentID != 0 {
		deployment, err := b.repo.Queries.GetDeployment(ctx, evt.DeploymentID)
		if err != nil {
			return
		}
		if deployment.Kind == "runbook" {
			execution, err := b.repo.Queries.GetRunbookExecutionByDeployment(
				ctx, evt.DeploymentID)
			if err != nil {
				return
			}
			switch evt.Type {
			case DeploymentStarted:
				evt.Type = RunbookStarted
			case DeploymentSucceeded:
				evt.Type = RunbookSucceeded
			case DeploymentFailed:
				evt.Type = RunbookFailed
			}
			evt.Message = strings.Replace(evt.Message,
				fmt.Sprintf("Deployment #%d", evt.DeploymentID),
				fmt.Sprintf("Runbook execution #%d", execution.ID), 1)
		} else if release, err := b.repo.Queries.GetRelease(
			ctx, deployment.ReleaseID); err == nil {
			source := provenance.FromRelease(release)
			for _, field := range []struct{ name, value string }{
				{"Repository", source.RepositoryURL}, {"Commit", source.CommitSHA},
				{"Artifact", source.ArtifactRef}, {"CI run", source.CIRunURL},
			} {
				if field.value != "" {
					evt.Message += "\n" + field.name + ": " + field.value
				}
			}
		}
	}
	settingsAvailable := true
	scope := "project"
	if evt.ProjectID == 0 {
		scope = "global"
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
		} else {
			settingsAvailable = false
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
	} else {
		settingsAvailable = false
	}
	logger := slog.With("event_type", evt.Type,
		"deployment_id", evt.DeploymentID, "project_id", evt.ProjectID,
		"environment_id", evt.EnvironmentID)
	if !settingsAvailable {
		logger.WarnContext(ctx, "load notification settings failed",
			"settings_scope", scope)
	}

	results := make(map[string]string, len(b.notifiers))
	for _, n := range b.notifiers {
		if !settingsAvailable {
			results[n.Name()] = "failed: delivery failed"
			continue
		}
		skipped, err := n.Notify(ctx, evt)
		switch {
		case skipped:
			results[n.Name()] = "skipped"
		case n.Name() == "email" && err == (SMTPPlaintextWarning{}):
			results[n.Name()] = SMTPPlaintextStatus
		case err != nil:
			results[n.Name()] = "failed: delivery failed"
		default:
			results[n.Name()] = "ok"
		}
	}
	resultsJSON, _ := json.Marshal(results)

	// FK columns (deployment_id/project_id/environment_id) must only be
	// marked Valid when an id was actually supplied — otherwise a zero
	// value would reference a nonexistent row 0 and fail the FK
	// constraint and prevent the history insert.
	_, err := b.repo.Queries.CreateNotificationEvent(
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
	if err != nil {
		logger.WarnContext(ctx, "write notification history failed")
	}
}
