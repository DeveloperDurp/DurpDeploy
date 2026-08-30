package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"time"
)

const defaultStepTimeout = 5 * time.Minute

var ErrCancelled = errors.New("step execution cancelled")

func baseStepEnv() []string {
	environment := []string{
		"PATH=/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin",
		"HOME=/nonexistent",
		"USER=" + runnerUsername,
		"LOGNAME=" + runnerUsername,
		"TERM=xterm",
	}
	if lang := os.Getenv("LANG"); lang != "" {
		environment = append(environment, "LANG="+lang)
	}
	return environment
}

// JobConfig supplies the values copied into an immutable Job.
type JobConfig struct {
	DeploymentID int64
	Name         string
	ScriptBody   string
	Timeout      time.Duration
	MaxRetries   int
	Environment  map[string]string
	Secrets      []string
}

// Job is an immutable bash step execution request.
type Job struct {
	deploymentID int64
	name         string
	scriptBody   string
	timeout      time.Duration
	maxRetries   int
	environment  map[string]string
	secrets      []string
}

func NewJob(config JobConfig) Job {
	environment := make(map[string]string, len(config.Environment))
	for key, value := range config.Environment {
		environment[key] = value
	}
	return Job{
		deploymentID: config.DeploymentID,
		name:         config.Name,
		scriptBody:   config.ScriptBody,
		timeout:      config.Timeout,
		maxRetries:   config.MaxRetries,
		environment:  environment,
		secrets:      slices.Clone(config.Secrets),
	}
}

// CallbacksConfig supplies callbacks copied into immutable Callbacks.
type CallbacksConfig struct {
	WriteLog            func(string) error
	TrackProcessGroup   func(int)
	UntrackProcessGroup func()
	Cancelled           func() bool
}

// Callbacks connect execution to the caller's logs and lifecycle state.
type Callbacks struct {
	writeLog            func(string) error
	trackProcessGroup   func(int)
	untrackProcessGroup func()
	cancelled           func() bool
}

func NewCallbacks(config CallbacksConfig) Callbacks {
	return Callbacks{
		writeLog:            config.WriteLog,
		trackProcessGroup:   config.TrackProcessGroup,
		untrackProcessGroup: config.UntrackProcessGroup,
		cancelled:           config.Cancelled,
	}
}

// Executor executes bash jobs with the local sandbox and process isolation.
type Executor struct {
	sandbox    *Sandbox
	sandboxErr error
}

func NewExecutor() *Executor {
	sandbox, err := newSandbox()
	return &Executor{sandbox: sandbox, sandboxErr: err}
}

func (e *Executor) Execute(
	ctx context.Context,
	job Job,
	callbacks Callbacks,
) error {
	if e.sandboxErr != nil {
		return fmt.Errorf("initialize runner sandbox: %w", e.sandboxErr)
	}
	writer := newRedactingWriter(NewScrubber(job.secrets), callbacks.writeLog)
	maxAttempts := job.maxRetries + 1
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = e.runAttempt(ctx, job, writer, callbacks, attempt)
		if lastErr == nil {
			return nil
		}
		if callbacks.cancelled != nil && callbacks.cancelled() {
			return ErrCancelled
		}
		if attempt < maxAttempts {
			if err := writer.write(
				fmt.Sprintf(
					"step %q: retrying (attempt %d of %d)\n",
					job.name,
					attempt+1,
					maxAttempts,
				),
			); err != nil {
				return err
			}
			if err := writer.flush(); err != nil {
				return err
			}
		}
	}
	return lastErr
}

func (e *Executor) runAttempt(
	runCtx context.Context,
	job Job,
	writer *redactingWriter,
	callbacks Callbacks,
	attempt int,
) (err error) {
	timeout := job.timeout
	if timeout <= 0 {
		timeout = defaultStepTimeout
	}
	stepCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp(
		"",
		fmt.Sprintf("durpdeploy-%d-*", job.deploymentID),
	)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := tmpDir + "/script.sh"
	if err := os.WriteFile(
		scriptPath,
		[]byte(job.scriptBody),
		0755,
	); err != nil {
		return err
	}

	chrooted, err := e.sandbox.setupChroot(tmpDir)
	if err != nil {
		return fmt.Errorf("setup runner sandbox: %w", err)
	}
	defer e.sandbox.teardownChroot(tmpDir)
	cmd := e.command(stepCtx, chrooted, tmpDir, scriptPath)
	cmd.Env = baseStepEnv()
	for key, value := range job.environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}
	cmd.WaitDelay = 15 * time.Second
	setPgid(cmd)
	e.sandbox.applyCredential(cmd)
	if err := e.sandbox.clearCapabilities(cmd, chrooted); err != nil {
		return err
	}
	if err := os.Chmod(tmpDir, 0711); err != nil {
		return err
	}

	group, err := e.sandbox.createCgroup(job.deploymentID)
	if err != nil {
		return fmt.Errorf("setup runner cgroup: %w", err)
	}
	defer func() {
		if cleanupErr := e.sandbox.removeCgroup(group); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup runner cgroup: %w", cleanupErr))
		}
	}()
	if err := e.sandbox.configureCgroup(cmd, group); err != nil {
		return fmt.Errorf("configure runner cgroup: %w", err)
	}
	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(&output, writer)
	cmd.Stderr = io.MultiWriter(&output, writer)
	if err := cmd.Start(); err != nil {
		if err := writer.flush(); err != nil {
			return err
		}
		return err
	}
	if callbacks.trackProcessGroup != nil {
		callbacks.trackProcessGroup(cmd.Process.Pid)
		if callbacks.untrackProcessGroup != nil {
			defer callbacks.untrackProcessGroup()
		}
	}
	go e.killAfterDeadline(stepCtx, cmd)
	err = cmd.Wait()
	if flushErr := writer.flush(); flushErr != nil {
		return flushErr
	}
	if err != nil {
		if stepCtx.Err() == context.DeadlineExceeded {
			if writeErr := writer.write(
				fmt.Sprintf(
					"step %q: attempt %d timed out after %s\n",
					job.name,
					attempt,
					timeout,
				),
			); writeErr != nil {
				return writeErr
			}
		} else {
			if writeErr := writer.write(
				fmt.Sprintf(
					"step %q: attempt %d failed: %v\n",
					job.name,
					attempt,
					err,
				),
			); writeErr != nil {
				return writeErr
			}
		}
		if flushErr := writer.flush(); flushErr != nil {
			return flushErr
		}
	}
	return err
}

func (e *Executor) command(
	ctx context.Context,
	chrooted bool,
	tmpDir, scriptPath string,
) *exec.Cmd {
	if !chrooted {
		cmd := exec.CommandContext(ctx, "bash", scriptPath)
		cmd.Dir = tmpDir
		return cmd
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "/script.sh")
	cmd.Dir = "/"
	e.sandbox.applyChroot(cmd, tmpDir)
	return cmd
}

func (e *Executor) killAfterDeadline(ctx context.Context, cmd *exec.Cmd) {
	<-ctx.Done()
	time.Sleep(10 * time.Second)
	if cmd.Process != nil {
		killProcessGroup(cmd.Process.Pid)
	}
}
