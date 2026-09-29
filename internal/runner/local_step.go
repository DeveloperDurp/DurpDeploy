package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/interpreter"
)

var errContainerCleanup = errors.New("container cleanup unconfirmed")

type localStepAttempt struct {
	deploymentID int64
	step         deploymentStep
	logWriter    *broadcastWriter
	environment  map[string]string
	attempt      int
}

func (r *DeploymentRunner) runStepAttempt(
	runCtx context.Context, request localStepAttempt,
) (result error) {
	if request.step.ContainerImage == "" {
		return fmt.Errorf(
			"step %q has no container image (legacy local steps cannot run on the host)",
			request.step.Name,
		)
	}
	if strings.HasPrefix(request.step.ContainerImage, "-") ||
		strings.ContainsAny(request.step.ContainerImage, " \t\r\n\x00") {
		return fmt.Errorf(
			"step %q has an invalid container image",
			request.step.Name,
		)
	}
	if r.localErr != nil {
		err := fmt.Errorf("initialize container execution: %w", r.localErr)
		_, _ = io.WriteString(request.logWriter, fmt.Sprintf(
			"step %q: attempt %d failed before execution: %v\n",
			request.step.Name, request.attempt, err,
		))
		request.logWriter.Flush()
		return err
	}
	selected, err := interpreter.Validate(request.step.Interpreter)
	if err != nil {
		return err
	}
	timeout := defaultStepTimeout
	if request.step.TimeoutSeconds > 0 {
		timeout = time.Duration(request.step.TimeoutSeconds) * time.Second
	}
	stepCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()
	if r.engine.kind == "docker" {
		err = r.rejectDockerImageVolumes(
			stepCtx,
			request.step.ContainerImage,
		)
		if err != nil {
			_, _ = io.WriteString(request.logWriter, fmt.Sprintf(
				"step %q: attempt %d failed before execution: %v\n",
				request.step.Name, request.attempt, err,
			))
			request.logWriter.Flush()
			return err
		}
	}
	var selectedEnv []string
	var envArgs []string
	for _, name := range request.step.VariableNames {
		value, exists := request.environment[name]
		if containerenv.ValidateName(name, true) != nil || !exists ||
			strings.ContainsRune(value, '\x00') {
			return fmt.Errorf(
				"invalid or unavailable selected variable %q",
				name,
			)
		}
		selectedEnv = append(selectedEnv, name+"="+value)
		envArgs = append(envArgs, "--env", name)
	}

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	name := fmt.Sprintf(
		"durpdeploy-%d-%d-%s",
		request.deploymentID,
		request.attempt,
		hex.EncodeToString(nonce),
	)
	args := []string{"run"}
	if r.engine.kind == "podman" {
		args = append(args, "--rm")
	}
	timeoutFlag := "--stop-timeout="
	if r.engine.kind == "podman" {
		timeoutFlag = "--timeout="
	}
	args = append(args,
		"--interactive", "--pull=never",
		timeoutFlag+fmt.Sprint(int64(timeout/time.Second)),
		"--log-driver=none",
		"--name="+name, "--label=io.durpdeploy.attempt="+name,
		"--label=io.durpdeploy.namespace="+r.engine.scope(),
		"--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65534:65534",
		"--pids-limit=128", "--memory=256m", "--cpus=1")
	if r.engine.kind == "podman" {
		args = append(args, "--image-volume=ignore", "--http-proxy=false")
	}
	args = append(args, envArgs...)
	args = append(args, "--entrypoint="+selected, request.step.ContainerImage)
	switch selected {
	case interpreter.Bash:
		args = append(args, "-s")
	case interpreter.PowerShell:
		args = append(args, "-NoProfile", "-NonInteractive", "-File", "-")
	case interpreter.Python:
		args = append(args, "-")
	}
	cmd := r.engine.command(stepCtx, args...)
	cmd.Env = append(cmd.Env, selectedEnv...)
	cmd.Stdin = strings.NewReader(request.step.ScriptBody)
	cmd.Stdout = request.logWriter
	cmd.Stderr = request.logWriter
	r.trackAttempt(request.deploymentID, name)
	defer r.untrackAttempt(request.deploymentID)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()
		output, err := r.engine.command(
			cleanupCtx, r.engine.removeArgs(name)...,
		).
			CombinedOutput()
		if err = r.engine.removalError(output, err); err != nil {
			result = errors.Join(
				result,
				fmt.Errorf(
					"%w: remove container %s: %v: %s",
					errContainerCleanup,
					name,
					err,
					strings.TrimSpace(string(output)),
				),
			)
			_, _ = io.WriteString(
				request.logWriter,
				fmt.Sprintf(
					"step %q: container cleanup unconfirmed: %v\n",
					request.step.Name,
					err,
				),
			)
		}
		request.logWriter.Flush()
	}()
	err = cmd.Run()
	if err != nil {
		if stepCtx.Err() == context.DeadlineExceeded {
			_, _ = io.WriteString(
				request.logWriter,
				fmt.Sprintf(
					"step %q: attempt %d timed out after %s\n",
					request.step.Name,
					request.attempt,
					timeout,
				),
			)
		} else if runCtx.Err() == nil {
			_, _ = io.WriteString(
				request.logWriter,
				fmt.Sprintf(
					"step %q: attempt %d failed: %v\n",
					request.step.Name,
					request.attempt,
					err,
				),
			)
		}
	}
	return err
}

func (r *DeploymentRunner) rejectDockerImageVolumes(
	ctx context.Context,
	image string,
) error {
	output, err := r.engine.command(
		ctx,
		"image", "inspect", "--format={{json .Config.Volumes}}", image,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"inspect container image: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	var volumes map[string]json.RawMessage
	if err := json.Unmarshal(output, &volumes); err != nil {
		return fmt.Errorf(
			"inspect container image volume metadata: %w",
			err,
		)
	}
	if len(volumes) != 0 {
		return errors.New(
			"container image declares writable volumes, which Docker cannot disable",
		)
	}
	return nil
}
