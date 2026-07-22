package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"durpdeploy/internal/events"
)

// StartLitestreamCheck begins a background goroutine that periodically executes
// a command to verify backup health. If the command fails, it publishes a
// single global BackupUnhealthy event (backup health is a system-wide
// concern, routed through global_notifications, not per-project settings).
//
// Configuration via environment variables:
// - DURPDEPLOY_LITESTREAM_CHECK_COMMAND: the shell command to run (e.g. "litestream ltx ...")
// - DURPDEPLOY_LITESTREAM_CHECK_INTERVAL: how often to check (default 1h)
func StartLitestreamCheck(ctx context.Context, bus *events.Bus) {
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
					publishBackupEvent(ctx, bus, events.BackupUnhealthy, fmt.Sprintf("Litestream backup health check failed: %v", err))
					wasUnhealthy = true
				} else if wasUnhealthy {
					slog.Info("litestream health check recovered")
					publishBackupEvent(ctx, bus, events.BackupHealthy, "Litestream backup health check recovered")
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

// publishBackupEvent publishes a single project-less event. Backup health
// is a system-wide concern, not a per-project one, so it is routed through
// the global_notifications settings (ProjectID left at its zero value)
// instead of fanning out to every project as before.
func publishBackupEvent(ctx context.Context, bus *events.Bus, typ events.Type, message string) {
	bus.Publish(ctx, events.Event{
		Type:    typ,
		Message: message,
	})
}
