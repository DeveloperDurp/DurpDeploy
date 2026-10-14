package runner

import (
	"context"
	"errors"

	"durpdeploy/internal/repository"
)

// Sparse files can expand far beyond their allocated staging space during tar
// export. Inspect allocation after step cleanup, before writing the host spool.
func (r *DeploymentRunner) validateArtifactGateExport(
	ctx context.Context, keeper string,
) error {
	command := r.engine.command(ctx, "exec", keeper, "sh", "-c", `
set -euo pipefail
find /stage -type f -exec stat -c '%s %b' {} + |
  awk '$1 > $2 * 512 { exit 1 }'
`)
	if err := command.Run(); err != nil {
		return errors.Join(
			repository.ErrArtifactGate,
			errors.New(
				"approval context contains sparse files or cannot be inspected",
			),
			err,
		)
	}
	return nil
}
