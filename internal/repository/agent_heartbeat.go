package repository

import (
	"context"

	"durpdeploy/internal/db"
)

func (r *Repository) HeartbeatAgent(
	ctx context.Context,
	arg db.HeartbeatAgentParams,
) (int64, error) {
	var changed int64
	err := withSQLiteBusyRetry(ctx, func() error {
		var err error
		changed, err = r.Queries.HeartbeatAgent(ctx, arg)
		return err
	})
	return changed, err
}
