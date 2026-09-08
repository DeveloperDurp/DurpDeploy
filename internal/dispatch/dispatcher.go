package dispatch

import (
	"context"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// Dispatcher owns remote dispatch maintenance. Execution is wired separately.
type Dispatcher struct {
	repository *repository.Repository
}

func New(repo *repository.Repository) *Dispatcher {
	return &Dispatcher{repository: repo}
}

func (d *Dispatcher) Maintain(ctx context.Context, now int64) error {
	err := d.repository.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.ExpireAgentPairings(ctx, now); err != nil {
			return err
		}
		_, err := q.ExpireRemoteClaims(ctx, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("expire agent pairings and unstarted claims: %w", err)
	}
	return nil
}
