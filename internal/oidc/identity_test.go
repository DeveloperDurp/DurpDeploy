package oidc

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestIdentity_UsesDurableIssuerAndSubject(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	user := seedIdentityUser(t, repo, db.CreateUserParams{
		Email: "person@example.com", PasswordHash: "local-hash",
		Name: "Person", Role: string(RoleViewer),
	})
	identity := testIdentity()
	if _, err := repo.Queries.CreateOIDCIdentity(
		context.Background(),
		db.CreateOIDCIdentityParams{
			Issuer: identity.Issuer, Subject: identity.Subject, UserID: user.ID,
		},
	); err != nil {
		t.Fatalf("create OIDC identity: %v", err)
	}

	// When
	got, err := ResolveIdentity(context.Background(), repo, identity)

	// Then
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if got.ID != user.ID {
		t.Fatalf("resolved user ID = %d, want %d", got.ID, user.ID)
	}
}

func TestIdentity_LinksVerifiedNormalizedEmail(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	user := seedIdentityUser(t, repo, db.CreateUserParams{
		Email: "Person@Example.com", PasswordHash: "local-hash",
		Name: "Legacy Person", Role: string(RoleAdmin),
	})
	identity := testIdentity()

	// When
	got, err := ResolveIdentity(context.Background(), repo, identity)

	// Then
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if got.ID != user.ID || got.Email != identity.Email ||
		got.PasswordHash != "local-hash" {
		t.Fatalf("linked user = %#v", got)
	}
	linked, err := repo.Queries.GetOIDCIdentity(
		context.Background(),
		db.GetOIDCIdentityParams{
			Issuer:  identity.Issuer,
			Subject: identity.Subject,
		},
	)
	if err != nil || linked.UserID != user.ID {
		t.Fatalf("linked identity = %#v, %v", linked, err)
	}
}

func TestIdentity_RejectsAmbiguousCaseInsensitiveEmail(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	seedIdentityUser(t, repo, db.CreateUserParams{
		Email: "Person@Example.com", PasswordHash: "first", Name: "First", Role: "viewer",
	})
	seedIdentityUser(t, repo, db.CreateUserParams{
		Email: "person@example.com", PasswordHash: "second", Name: "Second", Role: "viewer",
	})

	// When
	_, err := ResolveIdentity(context.Background(), repo, testIdentity())

	// Then
	if !errors.Is(err, ErrIdentityAmbiguous) {
		t.Fatalf("resolve identity error = %v, want ErrIdentityAmbiguous", err)
	}
	if countOIDCIdentities(t, repo) != 0 {
		t.Fatal("ambiguous email created an OIDC identity")
	}
}

func TestIdentity_JITCreatesPasswordlessUser(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	identity := testIdentity()

	// When
	got, err := ResolveIdentity(context.Background(), repo, identity)

	// Then
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if got.Email != identity.Email || got.Name != identity.Name ||
		got.Role != string(identity.Role) || got.PasswordHash != "" {
		t.Fatalf("JIT user = %#v", got)
	}
}

func TestIdentity_SynchronizesChangedClaims(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	identity := testIdentity()
	user := seedIdentityUser(t, repo, db.CreateUserParams{
		Email: "old@example.com", PasswordHash: "local-hash", Name: "Old Name", Role: "viewer",
	})
	linkIdentity(t, repo, identityLink{Identity: identity, UserID: user.ID})
	identity.Email = "new@example.com"
	identity.Name = "New Name"
	identity.Role = RoleDeployer

	// When
	_, err := ResolveIdentity(context.Background(), repo, identity)

	// Then
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	updated, err := repo.Queries.GetUserByID(context.Background(), user.ID)
	if err != nil || updated.Email != identity.Email ||
		updated.Name != identity.Name ||
		updated.Role != string(identity.Role) {
		t.Fatalf("updated user = %#v, %v", updated, err)
	}
}

func TestIdentity_InvalidatesSessionsOnRoleDowngrade(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	identity := testIdentity()
	identity.Role = RoleViewer
	user := seedIdentityUser(t, repo, db.CreateUserParams{
		Email: identity.Email, PasswordHash: "local-hash", Name: identity.Name, Role: "admin",
	})
	linkIdentity(t, repo, identityLink{Identity: identity, UserID: user.ID})
	seedIdentitySession(t, repo, identitySession{
		UserID: user.ID, ID: "downgrade-session",
	})

	// When
	_, err := ResolveIdentity(context.Background(), repo, identity)

	// Then
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	_, err = repo.Queries.GetSession(context.Background(), db.GetSessionParams{
		ID: "downgrade-session", ExpiresAt: 0,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf(
			"downgraded session lookup error = %v, want sql.ErrNoRows",
			err,
		)
	}
}

func TestIdentity_PreservesSessionsWhenRoleDoesNotChange(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	identity := testIdentity()
	user := seedIdentityUser(t, repo, db.CreateUserParams{
		Email: identity.Email, PasswordHash: "local-hash", Name: identity.Name,
		Role: string(identity.Role),
	})
	linkIdentity(t, repo, identityLink{Identity: identity, UserID: user.ID})
	seedIdentitySession(t, repo, identitySession{
		UserID: user.ID, ID: "unchanged-session",
	})

	// When
	_, err := ResolveIdentity(context.Background(), repo, identity)

	// Then
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if _, err := repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID: "unchanged-session", ExpiresAt: 0,
		},
	); err != nil {
		t.Fatalf("unchanged session lookup: %v", err)
	}
}

func TestIdentity_RejectsUnknownRole(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	identity := testIdentity()
	identity.Role = Role("unknown")

	// When
	_, err := ResolveIdentity(context.Background(), repo, identity)

	// Then
	if !errors.Is(err, ErrIdentityInvalid) {
		t.Fatalf("resolve identity error = %v, want ErrIdentityInvalid", err)
	}
	if countOIDCIdentities(t, repo) != 0 {
		t.Fatal("unknown role created an OIDC identity")
	}
}

func TestIdentity_RejectsUserAlreadyLinkedToAnotherIdentity(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	user := seedIdentityUser(t, repo, db.CreateUserParams{
		Email: "person@example.com", PasswordHash: "local-hash",
		Name: "Person", Role: "viewer",
	})
	linkIdentity(t, repo, identityLink{
		Identity: ClaimIdentity{
			Issuer: "https://other-id.example", Subject: "other-subject",
		},
		UserID: user.ID,
	})

	// When
	_, err := ResolveIdentity(context.Background(), repo, testIdentity())

	// Then
	if !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("resolve identity error = %v, want ErrIdentityConflict", err)
	}
}

func TestIdentity_CascadesWhenResolvedUserIsDeleted(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	identity := testIdentity()
	user, err := ResolveIdentity(context.Background(), repo, identity)
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}

	// When
	err = repo.Queries.DeleteUser(context.Background(), user.ID)

	// Then
	if err != nil {
		t.Fatalf("delete resolved user: %v", err)
	}
	_, err = repo.Queries.GetOIDCIdentity(
		context.Background(),
		db.GetOIDCIdentityParams{
			Issuer:  identity.Issuer,
			Subject: identity.Subject,
		},
	)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cascaded identity lookup error = %v, want sql.ErrNoRows", err)
	}
}
