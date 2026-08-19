package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"durpdeploy/internal/agentclient"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/runner"
)

const claimFileName = "current-claim.json"

type claimMarker struct {
	DeploymentID int64  `json:"deployment_id"`
	TokenHash    string `json:"token_hash"`
}

func main() {
	configuration, err := loadConfig()
	if err != nil {
		slog.Error("invalid agent configuration", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	if err := run(
		ctx,
		configuration,
	); err != nil &&
		!errors.Is(err, context.Canceled) {
		slog.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configuration config) error {
	client, err := agentclient.New(configuration.client)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Enroll(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		claim, err := client.Poll(ctx)
		if err != nil {
			return err
		}
		if claim == nil {
			continue
		}
		if err := executeClaim(ctx, client, *claim); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func executeClaim(
	ctx context.Context,
	client *agentclient.Client,
	claim agentproto.PollResponse,
) error {
	if err := persistClaim(client, claim); err != nil {
		return err
	}
	defer clearClaim(client)
	plaintext, err := client.DecodePayload(claim)
	if err != nil {
		return err
	}
	payload, err := decodePayload(plaintext, int64(claim.DeploymentID))
	if err != nil {
		return err
	}
	if err := client.Start(
		ctx,
		claim.DeploymentID,
		claim.ClaimToken,
	); err != nil {
		return err
	}
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cancelled := false
	var cancelMu sync.Mutex
	logs := newLogSender(client, claim)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(agentproto.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				response, heartbeatErr := client.Heartbeat(
					executionCtx,
					claim.DeploymentID,
					claim.ClaimToken,
				)
				if heartbeatErr != nil {
					cancel()
					return
				}
				if response.CancelRequested {
					cancelMu.Lock()
					cancelled = true
					cancelMu.Unlock()
					cancel()
					return
				}
			}
		}
	}()

	environment, secrets, err := payload.environment()
	if err == nil {
		executor := runner.NewExecutor()
		for _, step := range payload.Release.Steps {
			err = executor.Execute(executionCtx, runner.NewJob(runner.JobConfig{
				DeploymentID: int64(claim.DeploymentID), Name: step.Name,
				ScriptBody: step.ScriptBody, Timeout: step.timeout(),
				MaxRetries: int(
					step.MaxRetries,
				), Environment: environment, Secrets: secrets,
			}), runner.NewCallbacks(runner.CallbacksConfig{
				WriteLog: logs.Write,
				Cancelled: func() bool {
					cancelMu.Lock()
					defer cancelMu.Unlock()
					return cancelled
				},
			}))
			if err != nil {
				break
			}
		}
	}
	cancel()
	<-heartbeatDone
	if flushErr := logs.Flush(ctx); flushErr != nil && err == nil {
		err = flushErr
	}
	cancelMu.Lock()
	wasCancelled := cancelled || errors.Is(err, runner.ErrCancelled) ||
		ctx.Err() != nil
	cancelMu.Unlock()
	ackCtx, ackCancel := context.WithTimeout(
		context.Background(),
		agentproto.CancelAcknowledgementTimeout,
	)
	defer ackCancel()
	if wasCancelled {
		return client.Cancelled(ackCtx, claim.DeploymentID, claim.ClaimToken)
	}
	result := agentproto.ResultSucceeded
	if err != nil {
		result = agentproto.ResultFailed
	}
	return client.Result(ackCtx, claim.DeploymentID, agentproto.ResultRequest{
		ClaimToken: claim.ClaimToken, State: result,
	})
}

func persistClaim(
	client *agentclient.Client,
	claim agentproto.PollResponse,
) error {
	// The client state directory is already private; persist only a token hash.
	digest := sha256.Sum256([]byte(claim.ClaimToken))
	contents, err := json.Marshal(
		claimMarker{
			DeploymentID: int64(claim.DeploymentID),
			TokenHash:    fmt.Sprintf("%x", digest),
		},
	)
	if err != nil {
		return err
	}
	path := filepath.Join(client.StateDir(), claimFileName)
	return writePrivateFile(path, contents)
}

func clearClaim(client *agentclient.Client) {
	if err := os.Remove(
		filepath.Join(client.StateDir(), claimFileName),
	); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		slog.Error("remove claim marker", "err", err)
	}
}

func writePrivateFile(path string, contents []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() {
		if removeErr := os.Remove(
			temporaryPath,
		); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) &&
			err == nil {
			err = removeErr
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

type logSender struct {
	client *agentclient.Client
	claim  agentproto.PollResponse
	mu     sync.Mutex
	next   agentproto.LogSequence
	events []agentproto.LogEvent
}

func newLogSender(
	client *agentclient.Client,
	claim agentproto.PollResponse,
) *logSender {
	return &logSender{client: client, claim: claim, next: 1}
}

func (sender *logSender) Write(line string) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.events = append(
		sender.events,
		agentproto.LogEvent{Sequence: sender.next, Line: line},
	)
	sender.next++
	if len(sender.events) < agentproto.MaxLogEvents {
		return nil
	}
	return sender.flushLocked(context.Background())
}

func (sender *logSender) Flush(ctx context.Context) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.flushLocked(ctx)
}

func (sender *logSender) flushLocked(ctx context.Context) error {
	if len(sender.events) == 0 {
		return nil
	}
	if err := sender.client.Logs(
		ctx,
		sender.claim.DeploymentID,
		agentproto.LogBatchRequest{
			ClaimToken: sender.claim.ClaimToken, Events: sender.events,
		},
	); err != nil {
		return err
	}
	sender.events = nil
	return nil
}
