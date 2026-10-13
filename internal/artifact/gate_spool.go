package artifact

import (
	"context"
	"errors"
	"log/slog"
	"os"
)

// OpenGateSpool removes the directory entry before sensitive bytes are written.
// Closing the descriptor, including on process exit, releases the artifact.
func OpenGateSpool() (*os.File, error) {
	file, err := os.CreateTemp("", "durpdeploy-gate-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// SpoolCleanupError preserves completed work and reports cleanup failures.
func SpoolCleanupError(
	ctx context.Context,
	result, closeErr error,
) error {
	if closeErr == nil {
		return result
	}
	if result == nil {
		slog.WarnContext(ctx, "close temporary spool", "err", closeErr)
		return nil
	}
	return errors.Join(result, closeErr)
}
