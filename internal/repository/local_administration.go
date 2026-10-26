package repository

import (
	"context"
	"errors"

	"durpdeploy/internal/db"
)

// WithLocalAdministrationTx coordinates local user mutations across processes.
// Acquire the shared row before user locks, counts, or mutations.
func (r *Repository) WithLocalAdministrationTx(
	ctx context.Context, mutate func(*db.Queries) error,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			locked, err := q.LockLocalAdministration(ctx)
			if err != nil {
				return err
			}
			if locked != 1 {
				return errors.New("local administration lock is unavailable")
			}
			return mutate(q)
		})
	})
}
