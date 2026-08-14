package mfa

import (
	"context"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestFactorStoreMutations_invalidateBrowserSessionsAndPendingChallenges(
	t *testing.T,
) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, store *FactorStore, user db.User)
		mutate func(t *testing.T, store *FactorStore, user db.User)
	}{
		{
			name: "activate TOTP",
			mutate: func(t *testing.T, store *FactorStore, user db.User) {
				t.Helper()
				if _, err := store.ActivateTOTP(
					context.Background(), user.ID, "JBSWY3DPEHPK3PXP", 1,
				); err != nil {
					t.Fatalf("activate TOTP: %v", err)
				}
			},
		},
		{
			name: "create credential",
			mutate: func(t *testing.T, store *FactorStore, user db.User) {
				t.Helper()
				if _, err := store.CreateCredential(
					context.Background(), credentialParams(user.ID, []byte{1}),
				); err != nil {
					t.Fatalf("create credential: %v", err)
				}
			},
		},
		{
			name: "regenerate recovery",
			setup: func(t *testing.T, store *FactorStore, user db.User) {
				t.Helper()
				activateTOTPForInvalidationTest(t, store, user)
			},
			mutate: func(t *testing.T, store *FactorStore, user db.User) {
				t.Helper()
				if _, err := store.RegenerateRecovery(
					context.Background(),
					user.ID,
				); err != nil {
					t.Fatalf("regenerate recovery codes: %v", err)
				}
			},
		},
		{
			name: "delete TOTP",
			setup: func(t *testing.T, store *FactorStore, user db.User) {
				t.Helper()
				activateTOTPForInvalidationTest(t, store, user)
				if _, err := store.CreateCredential(
					context.Background(), credentialParams(user.ID, []byte{2}),
				); err != nil {
					t.Fatalf("create second factor: %v", err)
				}
			},
			mutate: func(t *testing.T, store *FactorStore, user db.User) {
				t.Helper()
				if err := store.DeleteTOTP(
					context.Background(),
					user.ID,
				); err != nil {
					t.Fatalf("delete TOTP: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			_, repo, _, user := newFactorStoreFixture(t)
			store := NewFactorStore(FactorStoreConfig{
				Repository: repo,
				Box:        newFactorBox(t, 0),
				TOTP:       NewTOTP(nil, nil),
			})
			if test.setup != nil {
				test.setup(t, store, user)
			}
			seedBrowserStateForFactorInvalidation(t, repo, user)

			// When
			test.mutate(t, store, user)

			// Then
			assertBrowserStateInvalidated(t, repo, user)
		})
	}
}

func activateTOTPForInvalidationTest(
	t *testing.T,
	store *FactorStore,
	user db.User,
) {
	t.Helper()
	if _, err := store.ActivateTOTP(
		context.Background(), user.ID, "JBSWY3DPEHPK3PXP", 1,
	); err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}
}

func seedBrowserStateForFactorInvalidation(
	t *testing.T,
	repo *repository.Repository,
	user db.User,
) {
	t.Helper()
	token, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("create session credentials: %v", err)
	}
	if _, err := repo.Queries.CreateSession(
		context.Background(),
		db.CreateSessionParams{
			ID:        token,
			UserID:    user.ID,
			CsrfToken: csrf,
			ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
		},
	); err != nil {
		t.Fatalf("create session: %v", err)
	}
	challenge, err := NewChallengeService(
		ChallengeServiceConfig{Repository: repo},
	).Issue(
		context.Background(),
		ChallengeIssue{UserID: user.ID, Purpose: ChallengePurposeTOTPVerify},
	)
	if err != nil || challenge.Token == "" {
		t.Fatalf("create MFA challenge: %v", err)
	}
}

func assertBrowserStateInvalidated(
	t *testing.T,
	repo *repository.Repository,
	user db.User,
) {
	t.Helper()
	var sessions, challenges int
	if err := repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		user.ID,
	).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		user.ID,
	).Scan(&challenges); err != nil {
		t.Fatalf("count MFA challenges: %v", err)
	}
	if sessions != 0 || challenges != 0 {
		t.Fatalf("sessions=%d challenges=%d, want 0", sessions, challenges)
	}
}
