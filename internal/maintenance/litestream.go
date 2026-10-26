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

	"durpdeploy/internal/config"
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
	settings, err := LoadCheckConfig(os.LookupEnv)
	if err != nil {
		return err
	}
	return StartLitestreamCheckWithConfig(ctx, bus, settings)
}

type CheckConfig struct {
	Command  string
	Interval time.Duration
}

func LoadCheckConfig(lookup config.Lookup) (CheckConfig, error) {
	command, _ := lookup("DURPDEPLOY_LITESTREAM_CHECK_COMMAND")
	if command == "" {
		return CheckConfig{}, nil
	}
	raw, _ := lookup("DURPDEPLOY_LITESTREAM_CHECK_INTERVAL")
	if raw == "" {
		raw = config.Defaults().Litestream.CheckInterval
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return CheckConfig{}, errors.New(
			"DURPDEPLOY_LITESTREAM_CHECK_INTERVAL must be a positive duration",
		)
	}
	return CheckConfig{Command: command, Interval: interval}, nil
}

// StartLitestreamCheckWithConfig uses settings validated before startup effects.
func StartLitestreamCheckWithConfig(
	ctx context.Context,
	bus *events.Bus,
	settings CheckConfig,
) error {
	if settings.Command == "" {
		return nil
	}
	if settings.Interval <= 0 {
		return errors.New("backup check interval must be positive")
	}
	go func() {
		ticker := time.NewTicker(settings.Interval)
		defer ticker.Stop()
		slog.Info(
			"litestream health check started",
			"interval",
			settings.Interval,
		)

		// track last state to avoid spamming "healthy" notifications
		wasUnhealthy := false

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := runCheck(ctx, settings.Command)
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
