package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"durpdeploy/internal/interpreter"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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
		return fmt.Errorf("initialize Podman execution: %w", r.localErr)
	}
	selected, err := interpreter.Validate(request.step.Interpreter)
	if err != nil {
		return err
	}
	var selectedEnv []string
	var envArgs []string
	for _, name := range request.step.VariableNames {
		value, exists := request.environment[name]
		if !envName.MatchString(name) || !exists ||
			strings.ContainsRune(value, '\x00') {
			return fmt.Errorf(
				"invalid or unavailable selected variable %q",
				name,
			)
		}
		selectedEnv = append(selectedEnv, name+"="+value)
		envArgs = append(envArgs, "--env", name)
	}

	timeout := defaultStepTimeout
	if request.step.TimeoutSeconds > 0 {
		timeout = time.Duration(request.step.TimeoutSeconds) * time.Second
	}
	stepCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()
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
	args := []string{
		"run", "--rm", "--interactive", "--pull=never",
		"--timeout=" + fmt.Sprint(int64(timeout/time.Second)),
		"--log-driver=none",
		"--name=" + name, "--label=io.durpdeploy.attempt=" + name,
		"--label=io.durpdeploy.namespace=" + r.podman.namespace,
		"--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65534:65534",
		"--pids-limit=128", "--memory=256m", "--image-volume=ignore",
		"--http-proxy=false",
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
	cmd := r.podman.command(stepCtx, args...)
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
		output, err := r.podman.command(cleanupCtx, "rm", "--force", "--time=0", "--ignore", name).
			CombinedOutput()
		if err != nil {
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
