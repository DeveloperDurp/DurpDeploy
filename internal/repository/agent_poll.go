package repository

import (
	"context"
	"fmt"

	"durpdeploy/internal/db"
)

func (r *Repository) RecordAgentPoll(
	ctx context.Context,
	heartbeat db.HeartbeatAgentParams,
	interpreters []string,
) (int64, error) {
	var changed int64
	err := withSQLiteBusyRetry(ctx, func() error {
		changed = 0
		return r.WithTx(ctx, func(q *db.Queries) error {
			var err error
			changed, err = q.HeartbeatAgent(ctx, heartbeat)
			if err != nil {
				return fmt.Errorf("record agent heartbeat: %w", err)
			}
			if changed == 0 {
				return nil
			}
			if _, err := q.DeleteAgentInterpreters(
				ctx,
				heartbeat.ID,
			); err != nil {
				return fmt.Errorf("replace agent interpreters: %w", err)
			}
			for _, value := range interpreters {
				if _, err := q.AddAgentInterpreter(
					ctx,
					db.AddAgentInterpreterParams{
						AgentID:     heartbeat.ID,
						Interpreter: value,
					},
				); err != nil {
					return fmt.Errorf("record agent interpreter: %w", err)
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
			return nil
		})
	})
	return changed, err
}
