package auth

import (
	"context"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

var ErrLastAdministrator = errors.New("at least one administrator must remain")

// DeleteAccount applies the local administrator guard and deletes atomically.
// Callers retain their self-deletion, membership, and transport policies.
func DeleteAccount(
	ctx context.Context, repo *repository.Repository, userID int64,
) error {
	return repo.WithLocalAdministrationTx(ctx, func(q *db.Queries) error {
		locked, err := q.LockMFAUser(ctx, userID)
		if err != nil {
			return fmt.Errorf("lock account: %w", err)
		}
		if locked == 0 {
			return nil
		}
		current, err := q.GetUserByID(ctx, userID)
		if err != nil {
			return err
		}
		if err := protectLastAdministrator(ctx, q, current); err != nil {
			return err
		}
		if err := InvalidateBrowserAuthInTx(ctx, q, userID); err != nil {
			return err
		}
		return q.DeleteUser(ctx, userID)
	})
}

func protectLastAdministrator(
	ctx context.Context, q *db.Queries, user db.User,
) error {
	if user.Role != "admin" {
		return nil
	}
	count, err := q.CountAdministrators(ctx)
	if err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastAdministrator
	}
	return nil
}
