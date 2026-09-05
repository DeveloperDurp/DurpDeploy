package repository_test

import (
	"context"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestRoutingSnapshot_TransactionRollbackAndInterruption(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	wantErr := errors.New("interrupt selection")
	err := repo.WithRoutingTx(ctx, func(queries *db.Queries) error {
		_, err := queries.CreateUser(ctx, db.CreateUserParams{
			Email: "routing-rollback@example.com", PasswordHash: "hash",
			Name: "Routing Rollback", Role: "admin",
		})
		if err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithRoutingTx() error = %v, want %v", err, wantErr)
	}
	_, err = repo.Queries.GetUserByEmail(ctx, "routing-rollback@example.com")
	if err == nil {
		t.Fatal("routing transaction committed after callback error")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = repo.WithRoutingTx(cancelled, func(*db.Queries) error {
		t.Fatal("callback ran after context cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled WithRoutingTx() error = %v", err)
	}
}
