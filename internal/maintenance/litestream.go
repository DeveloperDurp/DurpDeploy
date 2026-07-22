package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

// StartLitestreamCheck begins a background goroutine that periodically executes
// a command to verify backup health. If the command fails, it publishes a
// BackupUnhealthy event to the event bus for all projects.
//
// Configuration via environment variables:
// - DURPDEPLOY_LITESTREAM_CHECK_COMMAND: the shell command to run (e.g. "litestream ltx ...")
// - DURPDEPLOY_LITESTREAM_CHECK_INTERVAL: how often to check (default 1h)
func StartLitestreamCheck(ctx context.Context, repo *repository.Repository, bus *events.Bus) {
	command := os.Getenv("DURPDEPLOY_LITESTREAM_CHECK_COMMAND")
	if command == "" {
		return
	}

	interval := time.Hour
	if d, err := time.ParseDuration(os.Getenv("DURPDEPLOY_LITESTREAM_CHECK_INTERVAL")); err == nil {
		interval = d
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("litestream health check started", "interval", interval, "command", command)

		// track last state to avoid spamming "healthy" notifications
		wasUnhealthy := false

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := runCheck(ctx, command)
				if err != nil {
					slog.Error("litestream health check failed", "err", err)
					publishBackupEvent(ctx, repo, bus, events.BackupUnhealthy, fmt.Sprintf("Litestream backup health check failed: %v", err))
					wasUnhealthy = true
				} else if wasUnhealthy {
					slog.Info("litestream health check recovered")
					publishBackupEvent(ctx, repo, bus, events.BackupHealthy, "Litestream backup health check recovered")
					wasUnhealthy = false
				}
			}
		}
	}()
}

func runCheck(ctx context.Context, command string) error {
	// We use /bin/sh to allow pipes, logic, and env vars in the command string.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}

func publishBackupEvent(ctx context.Context, repo *repository.Repository, bus *events.Bus, typ events.Type, message string) {
	projects, err := repo.Queries.ListProjects(ctx)
	if err != nil {
		slog.Error("failed to list projects for backup notification", "err", err)
		return
	}

	if len(projects) == 0 {
		// No projects yet, just record to history with no associated project.
		// It won't reach any Slack/Email notifiers since they are project-scoped,
		// but admins can see it in /admin/notifications.
		bus.Publish(ctx, events.Event{
			Type:    typ,
			Message: message,
		})
		return
	}

	for _, p := range projects {
		bus.Publish(ctx, events.Event{
			Type:      typ,
			ProjectID: p.ID,
			Message:   message,
		})
	}
}
