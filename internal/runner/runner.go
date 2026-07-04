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

type DeploymentRunner struct {
	repo    *repository.Repository
	broker  *LogBroker
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

func New(repo *repository.Repository, broker *LogBroker) *DeploymentRunner {
	return &DeploymentRunner{
		repo:    repo,
		broker:  broker,
		cancels: make(map[int64]context.CancelFunc),
	}
}

func (r *DeploymentRunner) Broker() *LogBroker {
	return r.broker
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
	}
	if err := json.Unmarshal([]byte(release.StepsJson), &steps); err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}

	vars, err := r.repo.Queries.ListReleaseVariablesByRelease(ctx, releaseID)
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
		d := defaultStepTimeout
		if step.TimeoutSeconds > 0 {
			d = time.Duration(step.TimeoutSeconds) * time.Second
		}
		stepCtx, stepCancel := context.WithTimeout(runCtx, d)

		tmpDir, err := os.MkdirTemp(
			"",
			fmt.Sprintf("durpdeploy-%d-*", deploymentID),
		)
		if err != nil {
			stepCancel()
			_ = r.failUnlessCancelled(ctx, deploymentID)
			return
		}

		scriptPath := tmpDir + "/script.sh"
		if err := os.WriteFile(
			scriptPath,
			[]byte(step.ScriptBody),
			0755,
		); err != nil {
			os.RemoveAll(tmpDir)
			stepCancel()
			_ = r.failUnlessCancelled(ctx, deploymentID)
			return
		}

		cmd := exec.CommandContext(stepCtx, "bash", scriptPath)
		cmd.Dir = tmpDir
		cmd.Env = os.Environ()
		for k, v := range envMap {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.WaitDelay = 15 * time.Second

		var buf bytes.Buffer
		logWriter := &broadcastWriter{
			broker:       r.broker,
			repo:         r.repo,
			deploymentID: deploymentID,
			stepName:     step.Name,
			ctx:          ctx,
			secretValues: secretValues,
		}
		cmd.Stdout = io.MultiWriter(&buf, logWriter)
		cmd.Stderr = io.MultiWriter(&buf, logWriter)

		if err := cmd.Start(); err != nil {
			logWriter.Flush()
			os.RemoveAll(tmpDir)
			stepCancel()
			_ = r.failUnlessCancelled(ctx, deploymentID)
			return
		}

		go func() {
			<-stepCtx.Done()
			time.Sleep(10 * time.Second)
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}()

		err = cmd.Wait()
		logWriter.Flush()
		os.RemoveAll(tmpDir)
		stepCancel()

		if err != nil {
			if stepCtx.Err() == context.DeadlineExceeded {
				logWriter.Write(
					[]byte(fmt.Sprintf("step %q timed out after %s\n", step.Name, d)),
				)
				logWriter.Flush()
			}
			dep, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
			if dep.Status == "cancelled" {
				return
			}
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
