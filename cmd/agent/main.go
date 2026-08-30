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
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/runner"
)

const claimFileName = "current-claim.json"

var newExecutor = runner.NewExecutor

const agentHelpText = `Usage: durpdeploy-agent

Starts a local pairing listener until paired, then polls the persisted server.

Inputs:
  DURPDEPLOY_AGENT_LISTEN_ADDR  local address used only while pairing
  DURPDEPLOY_AGENT_STATE_DIR    private persistent state directory
  DURPDEPLOY_AGENT_VERSION      agent version sent after pairing

Pairing stores the server URL, pinned fingerprints, and agent ID in state.
Do not provide server connection settings manually.
`

type claimMarker struct {
	DeploymentID int64  `json:"deployment_id"`
	TokenHash    string `json:"token_hash"`
}

func main() {
	if len(os.Args) == 2 &&
		(os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Fprint(os.Stdout, agentHelp())
		return
	}
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

func agentHelp() string {
	return agentHelpText
}

func run(ctx context.Context, configuration config) error {
	for ctx.Err() == nil {
		client, err := agentclient.NewPaired(
			configuration.stateDir,
			configuration.agentVersion,
		)
		if errors.Is(err, agentstate.ErrRePairRequired) {
			if err := runBootstrap(ctx, configuration.bootstrap); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		for ctx.Err() == nil {
			claim, err := client.Poll(ctx)
			if err != nil {
				client.Close()
				return err
			}
			if claim == nil {
				continue
			}
			if err := executeClaim(ctx, client, *claim); err != nil {
				var statusErr *agentclient.StatusError
				if errors.As(err, &statusErr) && statusErr.Status == 409 {
					continue
				}
				client.Close()
				return err
			}
		}
		client.Close()
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
		executor := newExecutor()
		err = executor.ExecuteSteps(executionCtx, runner.ExecutionConfig{
			DeploymentID: int64(claim.DeploymentID),
			Steps:        payload.Release.Steps,
			Environment:  environment,
			Secrets:      secrets,
			CallbacksForStep: func(runner.Step) runner.Callbacks {
				return runner.NewCallbacks(runner.CallbacksConfig{
					WriteLog: logs.Write,
					Cancelled: func() bool {
						cancelMu.Lock()
						defer cancelMu.Unlock()
						return cancelled
					},
				})
			},
		})
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
