package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/config"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/stepdefinition"
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
	ansible      ansibleAttempt
}

func (r *DeploymentRunner) runStepAttempt(
	runCtx context.Context, request localStepAttempt,
) (result error) {
	if err := agentexecution.ValidateLimitsWithPolicy(
		request.step.TimeoutSeconds,
		request.step.MaxRetries,
		r.repo.ResourceLimits().Execution,
	); err != nil {
		return err
	}
	if !r.beginLocalWork() {
		return context.Canceled
	}
	defer r.localWork.Done()
	if request.logWriter.outputFailed {
		return errPendingLogLimit
	}
	if err := request.logWriter.state("running"); err != nil {
		return err
	}
	defer func() {
		request.logWriter.finishState(result, runCtx.Err() != nil)
	}()
	if err := request.step.validateLocalContainerImage(); err != nil {
		return err
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
	if err := stepdefinition.Definition(request.step).ValidateKind(); err != nil {
		return err
	}
	selected := "ansible-playbook"
	var err error
	if request.step.Kind != stepdefinition.AnsibleKind {
		selected, err = interpreter.Validate(request.step.Interpreter)
		if err != nil {
			return err
		}
	}
	host := r.engine.hostname(runCtx)
	limits := r.repo.ResourceLimits()
	timeout := time.Duration(
		limits.Execution.DefaultTimeoutSeconds,
	) * time.Second
	if request.step.TimeoutSeconds > 0 {
		timeout = time.Duration(request.step.TimeoutSeconds) * time.Second
	}
	stepCtx, cancel := context.WithTimeout(runCtx, timeout)
	request.logWriter.cancelAttempt = cancel
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
	if request.step.Kind == stepdefinition.AnsibleKind {
		request.ansible, err = r.prepareAnsibleAttempt(stepCtx, request)
		defer func() { result = errors.Join(result, r.cleanupAnsibleAttempt(request.ansible.stage)) }()
		if err != nil {
			_, logErr := fmt.Fprintf(
				request.logWriter,
				"step %q: Ansible preparation failed: %v\n",
				request.step.Name,
				err,
			)
			request.logWriter.Flush()
			return errors.Join(err, logErr)
		}
	}
	selectedEnv, envArgs, err := request.containerEnvironment()
	if err != nil {
		_, logErr := fmt.Fprintf(request.logWriter,
			"step %q: failed before execution: %v\n", request.step.Name, err)
		request.logWriter.Flush()
		return errors.Join(err, logErr)
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
	args := r.localContainerArgs(
		request,
		name,
		selected,
		timeout,
		envArgs,
		limits,
	)
	if _, err := io.WriteString(request.logWriter, fmt.Sprintf(
		"Execution location: target=server host=%q mode=container runtime=%q container=%q image=%q interpreter=%q attempt=%d\n",
		request.logWriter.scrubber.Scrub(host),
		r.engine.kind,
		name,
		request.step.ContainerImage,
		selected,
		request.attempt,
	)); err != nil {
		return fmt.Errorf("record execution location: %w", err)
	}
	cmd := r.engine.command(stepCtx, args...)
	cmd.Env = append(cmd.Env, selectedEnv...)
	if request.step.Kind != stepdefinition.AnsibleKind {
		cmd.Stdin = strings.NewReader(request.step.ScriptBody)
	}
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
		request.logWriter.Flush()
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
			line := request.logWriter.scrubber.Scrub(fmt.Sprintf(
				"step %q: container cleanup unconfirmed: %v: %s",
				request.step.Name,
				err,
				strings.TrimSpace(string(output)),
			))
			if persistErr := request.logWriter.writeLine(
				line,
				"",
			); persistErr != nil {
				result = errors.Join(result, fmt.Errorf(
					"persist container cleanup diagnostic: %w", persistErr,
				))
			}
		}
	}()
	err = cmd.Run()
	if request.logWriter.outputFailed {
		return errors.Join(errPendingLogLimit, err)
	}
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

func (r *DeploymentRunner) localContainerArgs(
	request localStepAttempt,
	name, selected string,
	timeout time.Duration,
	envArgs []string,
	limits config.Limits,
) []string {
	args := []string{"run"}
	if r.engine.kind == "podman" {
		args = append(args, "--rm")
	}
	timeoutFlag := "--stop-timeout="
	if r.engine.kind == "podman" {
		timeoutFlag = "--timeout="
	}
	network := request.step.NetworkMode
	tmpfs := fmt.Sprintf("--tmpfs=/tmp:rw,nosuid,size=%d",
		limits.Container.TmpfsBytes)
	if request.step.ApprovalArtifactPath != "" ||
		request.approved.volume != "" {
		// Providers execute in /tmp; staging and approved context stay noexec.
		tmpfs = fmt.Sprintf(
			"--tmpfs=/tmp:rw,exec,nosuid,nodev,size=%d",
			limits.Artifact.DownloadBytes+limits.Container.TmpfsBytes,
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
		fmt.Sprintf("--pids-limit=%d", r.repo.ResourceLimits().Container.PIDs))
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
	if request.ansible.stage.volume != "" {
		args = append(
			args,
			"--volume="+request.ansible.stage.volume+":"+ansibleMount+":ro,nocopy",
		)
	}
	args = append(args, "--entrypoint="+selected, request.step.ContainerImage)
	if request.step.Kind == stepdefinition.AnsibleKind {
		return append(args, request.ansible.args...)
	}
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
	return args
}
