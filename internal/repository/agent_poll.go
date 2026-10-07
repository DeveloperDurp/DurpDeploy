package repository

import (
	"context"
	"database/sql"
	"fmt"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (r *Repository) RecordAgentPoll(
	ctx context.Context,
	heartbeat db.HeartbeatAgentParams,
	request agentproto.PollRequest,
) (int64, error) {
	interpreters := request.SupportedInterpreters
	modes := request.ExecutionModes
	if request.Protocol == agentproto.AgentV1 {
		interpreters = []agentproto.Interpreter{agentproto.InterpreterBash}
	}
	if request.Protocol != agentproto.AgentV3 {
		modes = []agentproto.ExecutionMode{agentproto.ExecutionHost}
	}
	var changed int64
	err := withSQLiteBusyRetry(ctx, func() error {
		changed = 0
		return r.WithQueueMaintenanceTx(
			ctx,
			func(ctx context.Context, q *db.Queries) error {
				var err error
				changed, err = q.HeartbeatAgent(ctx, heartbeat)
				if err != nil {
					return fmt.Errorf("record agent heartbeat: %w", err)
				}
				if changed == 0 {
					return nil
				}
				if err := q.SetAgentProtocol(ctx, db.SetAgentProtocolParams{
					ID: heartbeat.ID,
					AgentProtocol: sql.NullString{
						String: string(request.Protocol), Valid: true,
					},
				}); err != nil {
					return fmt.Errorf("record agent protocol: %w", err)
				}
				if err := clearAgentCapabilities(ctx, q, heartbeat.ID); err != nil {
					return fmt.Errorf("replace agent interpreters: %w", err)
				}
				for _, value := range interpreters {
					if _, err := q.AddAgentInterpreter(
						ctx,
						db.AddAgentInterpreterParams{
							AgentID:     heartbeat.ID,
							Interpreter: string(value),
						},
					); err != nil {
						return fmt.Errorf("record agent interpreter: %w", err)
					}
				}
				for _, mode := range modes {
					if err := q.AddAgentExecutionMode(ctx, db.AddAgentExecutionModeParams{
						AgentID: heartbeat.ID, ExecutionMode: string(mode),
					}); err != nil {
						return fmt.Errorf("record execution mode: %w", err)
					}
				}
				for _, runtime := range request.ContainerRuntimes {
					if err := q.AddAgentContainerRuntime(ctx, db.AddAgentContainerRuntimeParams{
						AgentID: heartbeat.ID, Runtime: string(runtime),
					}); err != nil {
						return fmt.Errorf("record container runtime: %w", err)
					}
				}
				for _, interpreter := range request.ContainerInterpreters {
					if err := q.AddAgentContainerInterpreter(ctx, db.AddAgentContainerInterpreterParams{
						AgentID: heartbeat.ID, Interpreter: string(interpreter),
					}); err != nil {
						return fmt.Errorf(
							"record container interpreter: %w",
							err,
						)
					}
				}
				if request.Protocol == agentproto.AgentV3 &&
					len(request.ContainerRuntimes) > 0 {
					if err := q.ConfirmRemoteStepCleanup(ctx, db.ConfirmRemoteStepCleanupParams{
						Now: heartbeat.Now, AgentID: heartbeat.ID,
					}); err != nil {
						return fmt.Errorf(
							"confirm remote step cleanup: %w",
							err,
						)
					}
					if err := q.ConfirmRemoteDeploymentCleanup(ctx, db.ConfirmRemoteDeploymentCleanupParams{
						Now: heartbeat.Now, AgentID: heartbeat.ID,
					}); err != nil {
						return fmt.Errorf(
							"confirm remote deployment cleanup: %w",
							err,
						)
					}
				}
				if _, err := q.FailUnsupportedWaitingRemoteStepRuns(
					ctx,
					db.FailUnsupportedWaitingRemoteStepRunsParams{
						Now:     heartbeat.Now,
						AgentID: heartbeat.ID,
					},
				); err != nil {
					return fmt.Errorf("fail unsupported remote steps: %w", err)
				}
				if err := q.FailUnsupportedWaitingRemoteDeploymentClaims(ctx,
					db.FailUnsupportedWaitingRemoteDeploymentClaimsParams{
						Now: heartbeat.Now, AgentID: heartbeat.ID,
					}); err != nil {
					return fmt.Errorf("fail unsupported legacy claims: %w", err)
				}
				return q.FailUnsupportedRemoteDeploymentStatus(ctx,
					db.FailUnsupportedRemoteDeploymentStatusParams{
						Now: heartbeat.Now, AgentID: heartbeat.ID,
					})
			},
		)
	})
	return changed, err
}

func clearAgentCapabilities(
	ctx context.Context,
	q *db.Queries,
	agentID string,
) error {
	if _, err := q.DeleteAgentInterpreters(ctx, agentID); err != nil {
		return fmt.Errorf("clear host interpreters: %w", err)
	}
	if err := q.DeleteAgentExecutionModes(ctx, agentID); err != nil {
		return fmt.Errorf("clear execution modes: %w", err)
	}
	if err := q.DeleteAgentContainerRuntimes(ctx, agentID); err != nil {
		return fmt.Errorf("clear container runtimes: %w", err)
	}
	if err := q.DeleteAgentContainerInterpreters(ctx, agentID); err != nil {
		return fmt.Errorf("clear container interpreters: %w", err)
	}
	return nil
}
