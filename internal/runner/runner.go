package runner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

const (
	defaultStepTimeout = 5 * time.Minute
	serviceUsername    = "durpdeploy"
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

type DeploymentRunner struct {
	repo    *repository.Repository
	broker  *LogBroker
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
	// pgids tracks the process group of every step currently executing,
	// keyed by deployment ID. Populated in runStepAttempt, cleared when
	// the step exits. Used by KillAll to reap orphans on server shutdown.
	// ponytail: one entry per deployment (steps run sequentially, no
	// parallel step execution), so a plain map is enough.
	pgids      map[int64]int
	sandboxErr error
	// bus publishes deployment_started/succeeded/failed events for the
	// Slack/email notifiers (Stage 3). Nil until SetEventBus is called —
	// existing callers (tests, recovery path) that never call it simply
	// get no notifications, no other behavior change.
	bus *events.Bus
}

type deploymentStep struct {
	Name            string   `json:"name"`
	ScriptBody      string   `json:"script_body"`
	SortOrder       int64    `json:"sort_order"`
	TimeoutSeconds  int64    `json:"timeout_seconds"`
	MaxRetries      int64    `json:"max_retries"`
	ExecutionTarget string   `json:"execution_target"`
	AgentSelectors  []string `json:"agent_selectors"`
}

func New(repo *repository.Repository, broker *LogBroker) *DeploymentRunner {
	sandboxErr := validateExecutionBoundary()
	return &DeploymentRunner{
		repo:       repo,
		broker:     broker,
		cancels:    make(map[int64]context.CancelFunc),
		pgids:      make(map[int64]int),
		sandboxErr: sandboxErr,
	}
}

// SetEventBus wires the runner to publish deployment lifecycle events. Kept
// as a setter (instead of a New parameter) so every existing call site does
// not need to change; only cmd/server/main.go's real server startup calls it.
func (r *DeploymentRunner) SetEventBus(bus *events.Bus) {
	r.bus = bus
}

// KillAll SIGKILLs the process group of every step currently running,
// reaping their bash children so a server shutdown/restart never leaves
// orphaned deploy processes behind (P1-3). Safe to call with no deployments
// running.
func (r *DeploymentRunner) KillAll() {
	r.mu.Lock()
	pgids := make([]int, 0, len(r.pgids))
	for _, pgid := range r.pgids {
		pgids = append(pgids, pgid)
	}
	r.mu.Unlock()

	for _, pgid := range pgids {
		killProcessGroup(pgid)
	}
}

func (r *DeploymentRunner) Broker() *LogBroker {
	return r.broker
}

func (r *DeploymentRunner) trackProcessGroup(deploymentID int64, pgid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pgids[deploymentID] = pgid
}

func (r *DeploymentRunner) untrackProcessGroup(deploymentID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pgids, deploymentID)
}

func (r *DeploymentRunner) RegisterCancel(
	deploymentID int64,
	cancel context.CancelFunc,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[deploymentID] = cancel
}

func (r *DeploymentRunner) UnregisterCancel(deploymentID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cancels, deploymentID)
}

func (r *DeploymentRunner) Cancel(deploymentID int64) error {
	r.mu.Lock()
	cancel, ok := r.cancels[deploymentID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("deployment %d is not running", deploymentID)
	}

	cancel()

	now := time.Now().Unix()
	return r.repo.Queries.UpdateDeploymentStatus(
		context.Background(),
		db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     "cancelled",
			StartedAt:  sql.NullInt64{},
			FinishedAt: sql.NullInt64{Int64: now, Valid: true},
		},
	)
}

func (r *DeploymentRunner) runStepAttempt(
	ctx context.Context,
	runCtx context.Context,
	deploymentID int64,
	step deploymentStep,
	logWriter *broadcastWriter,
	envMap map[string]string,
	secretValues []string,
	attempt int,
) error {
	if r.sandboxErr != nil {
		return fmt.Errorf("initialize runner sandbox: %w", r.sandboxErr)
	}
	d := defaultStepTimeout
	if step.TimeoutSeconds > 0 {
		d = time.Duration(step.TimeoutSeconds) * time.Second
	}
	stepCtx, stepCancel := context.WithTimeout(runCtx, d)
	defer stepCancel()

	tmpDir, err := os.MkdirTemp(
		"",
		fmt.Sprintf("durpdeploy-%d-*", deploymentID),
	)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := tmpDir + "/script.sh"
	if err := os.WriteFile(
		scriptPath,
		[]byte(step.ScriptBody),
		0755,
	); err != nil {
		return err
	}

	cmd, err := r.command(stepCtx, tmpDir, scriptPath)
	if err != nil {
		return err
	}
	// Minimal, whitelisted environment (P1-4) instead of inheriting the
	// server's own os.Environ() — a step must not see DURPDEPLOY_DB,
	// DURPDEPLOY_SECRET_KEY, or anything else the server process holds.
	cmd.Env = baseStepEnv()
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.WaitDelay = 15 * time.Second

	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, logWriter)
	cmd.Stderr = io.MultiWriter(&buf, logWriter)

	if err := cmd.Start(); err != nil {
		logWriter.Flush()
		return err
	}

	r.trackProcessGroup(deploymentID, cmd.Process.Pid)
	defer r.untrackProcessGroup(deploymentID)

	go func() {
		<-stepCtx.Done()
		time.Sleep(10 * time.Second)
		if cmd.Process != nil {
			killProcessGroup(cmd.Process.Pid)
		}
	}()

	err = cmd.Wait()
	logWriter.Flush()

	timedOut := stepCtx.Err() == context.DeadlineExceeded
	if err != nil {
		if timedOut {
			logWriter.Write(
				[]byte(
					fmt.Sprintf(
						"step %q: attempt %d timed out after %s\n",
						step.Name,
						attempt,
						d,
					),
				),
			)
		} else {
			logWriter.Write(
				[]byte(
					fmt.Sprintf(
						"step %q: attempt %d failed: %v\n",
						step.Name,
						attempt,
						err,
					),
				),
			)
		}
		logWriter.Flush()
	}

	return err
}

func (r *DeploymentRunner) Run(
	ctx context.Context,
	deploymentID, releaseID, environmentID int64,
) {
	runCtx, cancel := context.WithCancel(ctx)
	r.RegisterCancel(deploymentID, cancel)
	defer r.UnregisterCancel(deploymentID)

	now := time.Now().Unix()

	_ = r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID:        deploymentID,
			Status:    "running",
			StartedAt: sql.NullInt64{Int64: now, Valid: true},
		},
	)

	release, err := r.repo.Queries.GetRelease(ctx, releaseID)
	if err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}

	envName := "unknown environment"
	if env, err := r.repo.Queries.GetEnvironment(
		ctx,
		environmentID,
	); err == nil {
		envName = env.Name
	}
	r.publish(
		ctx,
		events.DeploymentStarted,
		deploymentID,
		release.ProjectID,
		environmentID,
		fmt.Sprintf("Deployment #%d started on %s", deploymentID, envName),
	)

	var steps []deploymentStep
	stepSource, err := r.repo.Queries.GetDeploymentStepSource(ctx, deploymentID)
	if err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}
	if err := json.Unmarshal([]byte(stepSource.StepsJson), &steps); err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}

	vars, err := r.repo.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}

	resolved, err := ResolveReleaseVariables(vars, environmentID)
	if err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}
	envMap := make(map[string]string, len(resolved))
	var secretValues []string
	for _, variable := range resolved {
		envMap[variable.Name] = variable.Value
		if variable.Secret && variable.Value != "" {
			secretValues = append(secretValues, variable.Value)
		}
	}

	scrubber := NewScrubber(secretValues)

	for stepIndex, step := range steps {
		logWriter := &broadcastWriter{
			broker:       r.broker,
			repo:         r.repo,
			deploymentID: deploymentID,
			stepName:     step.Name,
			ctx:          ctx,
			scrubber:     scrubber,
		}
		if step.ExecutionTarget == "agent" {
			err := r.runRemoteStep(
				runCtx,
				deploymentID,
				int64(stepIndex),
				step,
				logWriter,
			)
			if err != nil {
				if runCtx.Err() != nil {
					return
				}
				r.failStep(ctx, deploymentID, release.ProjectID, environmentID, envName, err)
				return
			}
			continue
		}

		var lastErr error
		maxAttempts := int(step.MaxRetries) + 1
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			lastErr = r.runStepAttempt(
				ctx, runCtx, deploymentID, step,
				logWriter, envMap, secretValues, attempt,
			)
			if lastErr == nil {
				break
			}

			dep, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
			if dep.Status == "cancelled" {
				return
			}

			if attempt < maxAttempts {
				logWriter.Write(
					[]byte(
						fmt.Sprintf(
							"step %q: retrying (attempt %d of %d)\n",
							step.Name,
							attempt+1,
							maxAttempts,
						),
					),
				)
				logWriter.Flush()
			}
		}

		if lastErr != nil {
			r.failStep(ctx, deploymentID, release.ProjectID, environmentID, envName, lastErr)
			return
		}
	}

	dep, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
	if dep.Status == "cancelled" {
		return
	}
	_ = r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     "succeeded",
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
	r.publish(
		ctx,
		events.DeploymentSucceeded,
		deploymentID,
		release.ProjectID,
		environmentID,
		fmt.Sprintf("Deployment #%d succeeded on %s", deploymentID, envName),
	)
}

func (r *DeploymentRunner) runRemoteStep(
	ctx context.Context,
	deploymentID int64,
	stepIndex int64,
	step deploymentStep,
	logWriter *broadcastWriter,
) error {
	created, err := r.repo.QueueRemoteStepRuns(ctx, deploymentID, stepIndex)
	if err != nil {
		return err
	}
	if created == 0 {
		return fmt.Errorf(
			"step %q: no active paired agents match the environment and label",
			step.Name,
		)
	}
	_, _ = logWriter.Write([]byte(fmt.Sprintf(
		"step %q: queued for %d matching agent(s)\n",
		step.Name,
		created,
	)))
	logWriter.Flush()

	timeout := defaultStepTimeout
	if step.TimeoutSeconds > 0 {
		timeout = time.Duration(step.TimeoutSeconds) * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timedOut := false
	cancellationNeeded := false
	cancellationRequested := false
	failureAgent := ""
	for {
		if cancellationNeeded && !cancellationRequested {
			_, err := r.repo.Queries.RequestRemoteStepCancellation(
				context.Background(),
				db.RequestRemoteStepCancellationParams{
					Now: sql.NullInt64{
						Int64: time.Now().Unix(),
						Valid: true,
					},
					DeploymentID: deploymentID,
				},
			)
			if err != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
				continue
			}
			cancellationRequested = true
		}
		runs, err := r.repo.Queries.ListRemoteStepRuns(
			ctx,
			db.ListRemoteStepRunsParams{
				DeploymentID: deploymentID,
				StepIndex:    stepIndex,
			},
		)
		if err != nil {
			return err
		}
		allSucceeded := len(runs) > 0
		hasActive := false
		hasFailure := false
		for _, run := range runs {
			switch run.State {
			case "succeeded":
			case "failed", "cancelled", "lost", "cancel_unconfirmed":
				allSucceeded = false
				hasFailure = true
				if failureAgent == "" {
					failureAgent = run.AgentID
				}
			default:
				allSucceeded = false
				hasActive = true
			}
		}
		if hasFailure && hasActive {
			cancellationNeeded = true
		} else if hasFailure {
			if timedOut {
				return fmt.Errorf(
					"step %q timed out after %s",
					step.Name,
					timeout,
				)
			}
			return fmt.Errorf(
				"step %q failed on agent %s",
				step.Name,
				failureAgent,
			)
		}
		if allSucceeded {
			return nil
		}
		select {
		case <-ctx.Done():
			_, _ = r.repo.Queries.RequestRemoteStepCancellation(
				context.Background(),
				db.RequestRemoteStepCancellationParams{
					Now:          sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
					DeploymentID: deploymentID,
				},
			)
			return ctx.Err()
		case <-timer.C:
			if failureAgent == "" {
				timedOut = true
			}
			cancellationNeeded = true
		case <-ticker.C:
		}
	}
}

func (r *DeploymentRunner) failStep(
	ctx context.Context,
	deploymentID int64,
	projectID int64,
	environmentID int64,
	environmentName string,
	err error,
) {
	_ = r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID: deploymentID, Status: "failed",
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
	r.publish(
		ctx,
		events.DeploymentFailed,
		deploymentID,
		projectID,
		environmentID,
		fmt.Sprintf(
			"Deployment #%d failed on %s: %v",
			deploymentID,
			environmentName,
			err,
		),
	)
}

// publish is a no-op when the runner has no event bus wired (SetEventBus
// never called), so tests and the recovery path don't need a bus.
func (r *DeploymentRunner) publish(
	ctx context.Context,
	typ events.Type,
	deploymentID, projectID, environmentID int64,
	message string,
) {
	if r.bus == nil {
		return
	}
	r.bus.Publish(ctx, events.Event{
		Type:          typ,
		DeploymentID:  deploymentID,
		ProjectID:     projectID,
		EnvironmentID: environmentID,
		Message:       message,
	})
}

func (r *DeploymentRunner) failUnlessCancelled(
	ctx context.Context,
	deploymentID int64,
) error {
	dep, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
	if dep.Status == "cancelled" {
		return nil
	}
	return r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     "failed",
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
}

type broadcastWriter struct {
	broker       *LogBroker
	repo         *repository.Repository
	deploymentID int64
	stepName     string
	ctx          context.Context
	buf          bytes.Buffer
	scrubber     *Scrubber
}

// Write buffers output and scrubs everything up to the last newline before
// broadcasting/persisting it. Scrubbing the whole buffer (instead of a single
// line at a time) lets the Scrubber catch secrets that span multiple Write
// calls or contain embedded newlines (e.g. a multi-line SSH key).
func (w *broadcastWriter) Write(p []byte) (n int, err error) {
	w.buf.Write(p)
	data := w.buf.Bytes()
	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL == -1 {
		return len(p), nil
	}

	toScrub := string(data[:lastNL+1])
	scrubbed := w.scrubber.Scrub(toScrub)

	lines := strings.Split(strings.TrimSuffix(scrubbed, "\n"), "\n")
	for _, line := range lines {
		w.broker.Broadcast(w.deploymentID, line)
		w.writeLine(line)
	}

	w.buf.Next(lastNL + 1)
	return len(p), nil
}

func (w *broadcastWriter) Flush() {
	remaining := w.buf.String()
	if remaining != "" {
		remaining = w.scrubber.Scrub(remaining)
		w.broker.Broadcast(w.deploymentID, remaining)
		w.writeLine(remaining)
		w.buf.Reset()
	}
}

func (w *broadcastWriter) writeLine(line string) {
	_, _ = w.repo.Queries.CreateDeploymentLog(
		w.ctx,
		db.CreateDeploymentLogParams{
			DeploymentID: w.deploymentID,
			StepName:     sql.NullString{String: w.stepName, Valid: true},
			Line:         line,
		},
	)
}
