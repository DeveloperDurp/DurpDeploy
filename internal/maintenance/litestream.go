package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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
func StartLitestreamCheck(ctx context.Context, bus *events.Bus) error {
	command := os.Getenv("DURPDEPLOY_LITESTREAM_CHECK_COMMAND")
	if command == "" {
		return nil
	}

	interval := time.Hour
	if raw := os.Getenv("DURPDEPLOY_LITESTREAM_CHECK_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("parse backup check interval: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("backup check interval must be positive")
		}
		interval = d
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info(
			"litestream health check started",
			"interval",
			interval,
		)

		// track last state to avoid spamming "healthy" notifications
		wasUnhealthy := false

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := runCheck(ctx, command)
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					slog.Error("litestream health check failed", "err", err)
					publishBackupEvent(
						ctx,
						bus,
						events.BackupUnhealthy,
						fmt.Sprintf(
							"Litestream backup health check failed: %v",
							err,
						),
					)
					wasUnhealthy = true
				} else if wasUnhealthy {
					slog.Info("litestream health check recovered")
					publishBackupEvent(
						ctx,
						bus,
						events.BackupHealthy,
						"Litestream backup health check recovered",
					)
					wasUnhealthy = false
				}
			}
		}
	}()
	return nil
}

func runCheck(ctx context.Context, command string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// We use /bin/sh to allow pipes, logic, and env vars in the command string.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	configureCheckProcess(cmd)
	cmd.WaitDelay = time.Second
	output := &checkOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	if err != nil && cmd.Process != nil {
		// A child can hold output open after its shell exits.
		if cleanupErr := cmd.Cancel(); cleanupErr != nil &&
			!errors.Is(cleanupErr, os.ErrProcessDone) {
			err = errors.Join(err, cleanupErr)
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err,
			strings.ToValidUTF8(string(output.tail), "�"))
	}
	return nil
}

// checkOutput keeps a diagnostic tail. os/exec serializes shared stdout/stderr.
type checkOutput struct{ tail []byte }

func (w *checkOutput) Write(data []byte) (int, error) {
	const limit = 4096
	n := len(data)
	if n >= limit {
		w.tail = append(w.tail[:0], data[n-limit:]...)
		return n, nil
	}
	if overflow := len(w.tail) + n - limit; overflow > 0 {
		w.tail = w.tail[:copy(w.tail, w.tail[overflow:])]
	}
	w.tail = append(w.tail, data...)
	return n, nil
}

// publishBackupEvent publishes a single project-less event. Backup health
// is a system-wide concern, not a per-project one, so it is routed through
// the global_notifications settings (ProjectID left at its zero value)
// instead of fanning out to every project as before.
func publishBackupEvent(
	ctx context.Context,
	bus *events.Bus,
	typ events.Type,
	message string,
) {
	bus.Publish(ctx, events.Event{
		Type:    typ,
		Message: message,
	})
}
