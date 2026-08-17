package oidc

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestIdentity_GuardedSyncRejectsStaleRoleFromAnotherConnection(
	t *testing.T,
) {
	// Given
	first, second := newConcurrentIdentityRepositories(t)
	ctx := context.Background()
	user := seedIdentityUser(t, first, db.CreateUserParams{
		Email: "person@example.com", PasswordHash: "hash",
		Name: "Person", Role: "admin",
	})
	identity := testIdentity()
	linkIdentity(t, first, identityLink{Identity: identity, UserID: user.ID})
	seedIdentitySession(t, first, identitySession{
		UserID: user.ID, ID: "stale-role-session",
	})
	if _, err := ResolveIdentity(ctx, second, identity); err != nil {
		t.Fatalf("concurrent role sync: %v", err)
	}

	// When
	err := first.WithTx(ctx, func(queries *db.Queries) error {
		_, updateErr := queries.UpdateOIDCUser(ctx, db.UpdateOIDCUserParams{
			ID: user.ID, Email: user.Email, Name: user.Name,
			Role: "admin", ExpectedRole: "admin",
		})
		return updateErr
	})

	// Then
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale guarded update error = %v, want sql.ErrNoRows", err)
	}
	updated, err := second.Queries.GetUserByID(ctx, user.ID)
	if err != nil || updated.Role != "viewer" {
		t.Fatalf("user after stale update = %#v, %v", updated, err)
	}
	_, err = first.Queries.GetSession(ctx, db.GetSessionParams{
		ID: "stale-role-session", ExpiresAt: 0,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf(
			"session after role change error = %v, want sql.ErrNoRows",
			err,
		)
	}
}

func TestIdentity_GuardedSyncRollsBackOnEmailCollision(t *testing.T) {
	// Given
	first, second := newConcurrentIdentityRepositories(t)
	ctx := context.Background()
	user := seedIdentityUser(t, first, db.CreateUserParams{
		Email: "first@example.com", PasswordHash: "hash",
		Name: "First", Role: "admin",
	})
	seedIdentityUser(t, second, db.CreateUserParams{
		Email: "second@example.com", PasswordHash: "hash",
		Name: "Second", Role: "viewer",
	})

	// When
	err := first.WithTx(ctx, func(queries *db.Queries) error {
		_, updateErr := queries.UpdateOIDCUser(ctx, db.UpdateOIDCUserParams{
			ID: user.ID, Email: "second@example.com", Name: "Changed",
			Role: "viewer", ExpectedRole: "admin",
		})
		return updateErr
	})

	// Then
	if err == nil {
		t.Fatal("email collision update succeeded")
	}
	unchanged, err := second.Queries.GetUserByID(ctx, user.ID)
	if err != nil || unchanged.Email != "first@example.com" ||
		unchanged.Name != "First" || unchanged.Role != "admin" {
		t.Fatalf("user after collision = %#v, %v", unchanged, err)
	}
}
