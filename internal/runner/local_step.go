package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"durpdeploy/internal/interpreter"
)

// baseStepEnv returns the minimal environment passed to every step (P1-4)
// instead of inheriting the server's os.Environ(), which would otherwise
// leak DURPDEPLOY_DB, DURPDEPLOY_SECRET_KEY, and anything else the server
// process holds. Project/step variables are appended by the caller.
func baseStepEnv() []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin",
		"HOME=/nonexistent",
		"USER=" + serviceUsername,
		"LOGNAME=" + serviceUsername,
		"TERM=xterm",
	}
	if lang := os.Getenv("LANG"); lang != "" {
		env = append(env, "LANG="+lang)
	}
	return env
}

type localStepAttempt struct {
	deploymentID int64
	step         deploymentStep
	logWriter    *broadcastWriter
	environment  map[string]string
	attempt      int
}

func (r *DeploymentRunner) runStepAttempt(
	runCtx context.Context,
	request localStepAttempt,
) error {
	if r.sandboxErr != nil {
		return fmt.Errorf("initialize runner sandbox: %w", r.sandboxErr)
	}
	d := defaultStepTimeout
	if request.step.TimeoutSeconds > 0 {
		d = time.Duration(request.step.TimeoutSeconds) * time.Second
	}
	stepCtx, stepCancel := context.WithTimeout(runCtx, d)
	defer stepCancel()

	tmpDir, err := os.MkdirTemp(
		"",
		fmt.Sprintf("durpdeploy-%d-*", request.deploymentID),
	)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	selected, err := interpreter.Validate(request.step.Interpreter)
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(
		tmpDir,
		"script"+interpreter.Extension(selected),
	)
	if err := os.WriteFile(
		scriptPath,
		[]byte(request.step.ScriptBody),
		0755,
	); err != nil {
		return err
	}

	cmd, err := r.command(stepCtx, tmpDir, scriptPath, selected)
	if err != nil {
		_, _ = request.logWriter.Write([]byte(fmt.Sprintf(
			"step %q: attempt %d failed before execution: %v\n",
			request.step.Name,
			request.attempt,
			err,
		)))
		request.logWriter.Flush()
		return err
	}
	// Minimal, whitelisted environment (P1-4) instead of inheriting the
	// server's own os.Environ() — a step must not see DURPDEPLOY_DB,
	// DURPDEPLOY_SECRET_KEY, or anything else the server process holds.
	cmd.Env = baseStepEnv()
	for k, v := range request.environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.WaitDelay = 15 * time.Second

	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, request.logWriter)
	cmd.Stderr = io.MultiWriter(&buf, request.logWriter)

	if err := cmd.Start(); err != nil {
		request.logWriter.Flush()
		return err
	}

	r.trackProcessGroup(request.deploymentID, cmd.Process.Pid)
	defer r.untrackProcessGroup(request.deploymentID)

	err = cmd.Wait()
	killProcessGroup(cmd.Process.Pid)
	request.logWriter.Flush()

	timedOut := stepCtx.Err() == context.DeadlineExceeded
	if err != nil {
		if timedOut {
			request.logWriter.Write(
				[]byte(
					fmt.Sprintf(
						"step %q: attempt %d timed out after %s\n",
						request.step.Name,
						request.attempt,
						d,
					),
				),
			)
		} else {
			request.logWriter.Write(
				[]byte(
					fmt.Sprintf(
						"step %q: attempt %d failed: %v\n",
						request.step.Name,
						request.attempt,
						err,
					),
				),
			)
		}
		request.logWriter.Flush()
	}

	return err
}
