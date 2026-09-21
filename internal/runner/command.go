package runner

import (
	"context"
	"fmt"
	"os/exec"
)

func (r *DeploymentRunner) command(
	ctx context.Context, tmpDir, scriptPath, interpreter string,
) (*exec.Cmd, error) {
	executable, err := exec.LookPath(interpreter)
	if err != nil {
		return nil, fmt.Errorf(
			"interpreter %q is not installed: %w",
			interpreter,
			err,
		)
	}
	cmd := exec.CommandContext(ctx, executable, scriptPath)
	cmd.Dir = tmpDir
	// The process group lets cancellation and shutdown reap the complete tree.
	setPgid(cmd)
	return cmd, nil
}
