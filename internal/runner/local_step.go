package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"durpdeploy/internal/artifact"
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
	artifact     artifactStage
	verification bool
	handoff      artifactStage
	approved     artifactStage
}

func (r *DeploymentRunner) runStepAttempt(
	runCtx context.Context, request localStepAttempt,
) (result error) {
	if !r.beginLocalWork() {
		return context.Canceled
	}
	defer r.localWork.Done()
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
	if _, exists := request.environment[containerenv.StageVariable]; exists {
		return fmt.Errorf(
			"%s conflicts with a snapshotted variable",
			containerenv.StageVariable,
		)
	}
	for _, stage := range []artifactStage{request.artifact, request.handoff, request.approved} {
		if stage.volume == "" {
			continue
		}
		output, err := r.engine.command(runCtx, "inspect", "--format={{.State.Running}}", stage.keeper).
			Output()
		if err != nil || strings.TrimSpace(string(output)) != "true" {
			return errors.New("staging container is unavailable")
		}
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
	variableNames := request.step.VariableNames
	if len(variableNames) == 0 {
		variableNames = make([]string, 0, len(request.environment))
		for name := range request.environment {
			if request.validExecutionVariable(name) {
				variableNames = append(variableNames, name)
			}
		}
		sort.Strings(variableNames)
	}
	var selectedEnv []string
	var envArgs []string
	for _, name := range variableNames {
		value, exists := request.environment[name]
		if !request.validExecutionVariable(name) || !exists ||
			strings.ContainsRune(value, '\x00') {
			return fmt.Errorf(
				"invalid or unavailable selected variable %q",
				name,
			)
		}
		selectedEnv = append(selectedEnv, name+"="+value)
		envArgs = append(envArgs, "--env", name)
	}

	if request.artifact.volume != "" {
		selectedEnv = append(selectedEnv, "ARTIFACT_PATH="+artifactMount)
		envArgs = append(envArgs, "--env", "ARTIFACT_PATH")
	}
	if request.handoff.volume != "" {
		selectedEnv = append(
			selectedEnv,
			containerenv.StageVariable+"="+deploymentStageMount,
		)
		envArgs = append(envArgs, "--env", containerenv.StageVariable)
	}
	if request.approved.volume != "" {
		selectedEnv = append(
			selectedEnv,
			"DURPDEPLOY_APPROVED_DIR="+approvedMount,
		)
		envArgs = append(envArgs, "--env", "DURPDEPLOY_APPROVED_DIR")
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
	memoryLimit := "--memory=256m"
	if request.handoff.volume != "" {
		// The writing attempt is charged for shared tmpfs pages.
		memoryLimit = fmt.Sprintf(
			"--memory=%d", artifactStagingCapacity()+(256<<20),
		)
	}
	network := request.step.NetworkMode
	tmpfs := "--tmpfs=/tmp:rw,nosuid,size=64m"
	if request.step.ApprovalArtifactPath != "" ||
		request.approved.volume != "" {
		// Providers execute in /tmp; staging and approved context stay noexec.
		tmpfs = fmt.Sprintf(
			"--tmpfs=/tmp:rw,nosuid,size=%d",
			artifact.MaxDownload+(64<<20),
		)
	}
	if network == "" {
		network = "none"
	}
	args = append(args,
		"--interactive", "--pull=missing",
		timeoutFlag+fmt.Sprint(int64(timeout/time.Second)),
		"--log-driver=none",
		"--name="+name, "--label=io.durpdeploy.attempt="+name,
		"--label=io.durpdeploy.namespace="+r.engine.scope(),
		"--network="+network, "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65534:65534",
		tmpfs,
		"--env=HOME=/tmp", "--env=TERM=dumb",
		"--pids-limit=128", memoryLimit, "--cpus=1")
	if r.engine.kind == "podman" {
		args = append(args, "--image-volume=ignore", "--http-proxy=false")
	}
	args = append(args, envArgs...)
	args = append(args, r.artifactMountArgs(request.artifact)...)
	args = append(args, r.deploymentMountArgs(request.handoff)...)
	if request.approved.volume != "" {
		args = append(
			args,
			"--volume="+request.approved.volume+":"+approvedMount+":ro,nocopy",
		)
	}
	args = append(args, "--entrypoint="+selected, request.step.ContainerImage)
	switch selected {
	case interpreter.Bash:
		if request.verification {
			args = append(args, "-p")
		}
		args = append(args, "-s")
	case interpreter.PowerShell:
		args = append(
			args,
			"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "-",
		)
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

// Existing immutable snapshots may contain this name from before packages.
// New-write validation stays reserved; a mounted package owns the variable.
func (request localStepAttempt) validExecutionVariable(name string) bool {
	if request.verification {
		if strings.HasPrefix(name, "LD_") {
			return false
		}
		switch name {
		case "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH",
			"GLOBIGNORE", "PS4", "GCONV_PATH", "GLIBC_TUNABLES":
			return false
		}
	}
	if name == "ARTIFACT_PATH" && request.artifact.volume == "" {
		return true
	}
	return containerenv.ValidateName(name, true) == nil
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
				pullErr,
				strings.TrimSpace(string(pullOutput)),
			)
		}
		output, err = inspect()
		if err != nil {
			return fmt.Errorf(
				"inspect container image: %w: %s",
				err,
				strings.TrimSpace(string(output)),
			)
		}
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
