package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (step deploymentStep) validateLocalContainerImage() error {
	if step.ContainerImage == "" {
		return fmt.Errorf(
			"step %q has no container image (legacy local steps cannot run on the host)",
			step.Name,
		)
	}
	if strings.HasPrefix(step.ContainerImage, "-") ||
		strings.ContainsAny(step.ContainerImage, " \t\r\n\x00") {
		return fmt.Errorf("step %q has an invalid container image", step.Name)
	}
	return nil
}

func (r *DeploymentRunner) rejectDockerImageVolumes(
	ctx context.Context,
	image string,
) error {
	inspect := func() ([]byte, error) {
		return r.engine.command(
			ctx,
			"image", "inspect", "--format={{json .Config.Volumes}}", image,
		).CombinedOutput()
	}
	output, err := inspect()
	if err != nil {
		pullOutput, pullErr := r.engine.command(
			ctx, "pull", "--quiet", image,
		).CombinedOutput()
		if pullErr != nil {
			return fmt.Errorf(
				"pull container image: %w: %s",
				pullErr, strings.TrimSpace(string(pullOutput)),
			)
		}
		output, err = inspect()
		if err != nil {
			return fmt.Errorf(
				"inspect container image: %w: %s",
				err, strings.TrimSpace(string(output)),
			)
		}
	}
	var volumes map[string]json.RawMessage
	if err := json.Unmarshal(output, &volumes); err != nil {
		return fmt.Errorf("inspect container image volume metadata: %w", err)
	}
	if len(volumes) != 0 {
		return errors.New(
			"container image declares writable volumes, which Docker cannot disable",
		)
	}
	return nil
}
