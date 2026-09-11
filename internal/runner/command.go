package runner

import (
	"context"
	"os/exec"
)

func (r *DeploymentRunner) command(
	ctx context.Context,
	tmpDir, scriptPath string,
) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	cmd.Dir = tmpDir
	// The process group lets cancellation and shutdown reap the complete tree.
	setPgid(cmd)
	r.sandbox.applyCredential(cmd)
	if err := r.sandbox.clearCapabilities(cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}
