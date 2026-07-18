package runner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const defaultStepTimeout = 5 * time.Minute

// baseStepEnv returns the minimal environment passed to every step (P1-4)
// instead of inheriting the server's os.Environ(), which would otherwise
// leak DURPDEPLOY_DB, DURPDEPLOY_SECRET_KEY, and anything else the server
// process holds. Project/step variables are appended by the caller.
func baseStepEnv() []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin",
		"HOME=/nonexistent",
		"USER=" + runnerUsername,
		"LOGNAME=" + runnerUsername,
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
	pgids map[int64]int
	// sandbox drops each step's process to the low-privileged
	// durpdeploy-runner user (P1-4). Resolved once at startup; a no-op if
	// that account/platform isn't available (see sandbox_linux.go).
	sandbox *Sandbox
}

func New(repo *repository.Repository, broker *LogBroker) *DeploymentRunner {
	return &DeploymentRunner{
		repo:    repo,
		broker:  broker,
		cancels: make(map[int64]context.CancelFunc),
		pgids:   make(map[int64]int),
		sandbox: newSandbox(),
	}
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
	step struct {
		Name           string `json:"name"`
		ScriptBody     string `json:"script_body"`
		SortOrder      int64  `json:"sort_order"`
		TimeoutSeconds int64  `json:"timeout_seconds"`
		MaxRetries     int64  `json:"max_retries"`
	},
	logWriter *broadcastWriter,
	envMap map[string]string,
	secretValues []string,
	attempt int,
) error {
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

	// Bind-mount /bin, /usr, /lib, /lib64 (read-only) into tmpDir and chroot
	// the step into it (P1-4), so a step cannot see the rest of the host
	// filesystem (the DB, secret key, other projects' scratch dirs, ...).
	// Falls back to running un-chrooted (same as before P1-4) if bind
	// mounts aren't permitted, e.g. local dev without CAP_SYS_ADMIN.
	chrooted := r.sandbox.setupChroot(tmpDir)
	defer r.sandbox.teardownChroot(tmpDir)

	var cmd *exec.Cmd
	if chrooted {
		// Paths are relative to the chroot: the script lands at /script.sh,
		// bash at /bin/bash (bind-mounted above).
		cmd = exec.CommandContext(stepCtx, "/bin/bash", "/script.sh")
		cmd.Dir = "/"
		r.sandbox.applyChroot(cmd, tmpDir)
	} else {
		cmd = exec.CommandContext(stepCtx, "bash", scriptPath)
		cmd.Dir = tmpDir
	}
	// Minimal, whitelisted environment (P1-4) instead of inheriting the
	// server's own os.Environ() — a step must not see DURPDEPLOY_DB,
	// DURPDEPLOY_SECRET_KEY, or anything else the server process holds.
	cmd.Env = baseStepEnv()
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.WaitDelay = 15 * time.Second
	// Run the step in its own process group so a timeout/cancel/shutdown
	// can kill the whole tree (bash + anything it spawned) instead of just
	// the bash PID, which otherwise leaves grandchildren orphaned (P1-3).
	// setPgid/killProcessGroup are platform-specific (see procgroup_unix.go
	// / procgroup_other.go) so this package builds on non-Unix targets too.
	setPgid(cmd)
	// Drop to the durpdeploy-runner UID/GID (P1-4); no-op if that account
	// isn't provisioned or the platform doesn't support it.
	r.sandbox.applyCredential(cmd)

	// Allow the durpdeploy-runner user to enter the scratch directory (P1-4).
	// MkdirTemp creates it as 0700; we need 0711 (+x) at minimum.
	if err := os.Chmod(tmpDir, 0711); err != nil {
		return err
	}

	// Create the deployment's cgroup up front so the process can be moved
	// into it right after Start(); "" if cgroups aren't set up (P1-4).
	cgroup := r.sandbox.createCgroup(deploymentID)

	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, logWriter)
	cmd.Stderr = io.MultiWriter(&buf, logWriter)

	if err := cmd.Start(); err != nil {
		logWriter.Flush()
		r.sandbox.removeCgroup(cgroup)
		return err
	}

	r.trackProcessGroup(deploymentID, cmd.Process.Pid)
	defer r.untrackProcessGroup(deploymentID)

	// Move the process into its cgroup right after Start() (the PID must
	// exist first), and drop the cgroup once the step exits so cgroups
	// don't accumulate across deployments (P1-4).
	r.sandbox.addProcess(cgroup, cmd.Process.Pid)
	defer r.sandbox.removeCgroup(cgroup)

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

	var steps []struct {
		Name           string `json:"name"`
		ScriptBody     string `json:"script_body"`
		SortOrder      int64  `json:"sort_order"`
		TimeoutSeconds int64  `json:"timeout_seconds"`
		MaxRetries     int64  `json:"max_retries"`
	}
	if err := json.Unmarshal([]byte(release.StepsJson), &steps); err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}

	vars, err := r.repo.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}

	envMap := make(map[string]string)
	var secretValues []string
	for _, v := range vars {
		if v.EnvironmentID.Valid && v.EnvironmentID.Int64 == environmentID {
			envMap[v.Name] = v.Value.String
			if v.Secret != 0 && v.Value.String != "" {
				secretValues = append(secretValues, v.Value.String)
			}
		} else if !v.EnvironmentID.Valid {
			envMap[v.Name] = v.Value.String
			if v.Secret != 0 && v.Value.String != "" {
				secretValues = append(secretValues, v.Value.String)
			}
		}
	}

	for _, step := range steps {
		logWriter := &broadcastWriter{
			broker:       r.broker,
			repo:         r.repo,
			deploymentID: deploymentID,
			stepName:     step.Name,
			ctx:          ctx,
			secretValues: secretValues,
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
			_ = r.repo.Queries.UpdateDeploymentStatus(
				ctx,
				db.UpdateDeploymentStatusParams{
					ID:     deploymentID,
					Status: "failed",
					FinishedAt: sql.NullInt64{
						Int64: time.Now().Unix(),
						Valid: true,
					},
				},
			)
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
	secretValues []string
}

func (w *broadcastWriter) redact(s string) string {
	for _, secret := range w.secretValues {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return s
}

func (w *broadcastWriter) Write(p []byte) (n int, err error) {
	w.buf.Write(p)
	for {
		idx := bytes.IndexByte(w.buf.Bytes(), '\n')
		if idx == -1 {
			break
		}
		line := string(w.buf.Next(idx + 1))
		line = strings.TrimSuffix(line, "\n")
		line = w.redact(line)
		w.broker.Broadcast(w.deploymentID, line)
		w.writeLine(line)
	}
	return len(p), nil
}

func (w *broadcastWriter) Flush() {
	remaining := w.buf.String()
	if remaining != "" {
		remaining = w.redact(remaining)
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
