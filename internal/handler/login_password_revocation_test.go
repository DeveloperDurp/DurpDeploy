package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/testutil"
)

func TestPasswordLoginRevocationParity(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			conn, err := migrate.Run(projectUpdateDSN(t, backend))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Close(t, conn) })
			repo := repository.New(conn)
			for _, mutation := range []string{"password", "factor"} {
				t.Run(mutation+"/before issuance", func(t *testing.T) {
					checkPasswordLoginMutationFirst(t, repo, mutation)
				})
				t.Run(mutation+"/after issuance", func(t *testing.T) {
					checkPasswordLoginIssuanceFirst(t, repo, mutation)
				})
			}
			t.Run("revoke pending challenge", func(t *testing.T) {
				checkPasswordLoginPendingRevocation(t, repo)
			})
		})
	}
}

func checkPasswordLoginMutationFirst(
	t *testing.T, repo *repository.Repository, mutation string,
) {
	t.Helper()
	user := passwordProofUser(t, repo)
	mutatePasswordLoginUser(t, repo, user.ID, mutation)
	session, pending, err := completePasswordProof(t, repo, user)
	if session.ID != "" {
		t.Fatal("only a current password without MFA can issue a session")
	}
	if mutation == "password" {
		if !errors.Is(err, auth.ErrPasswordChanged) || pending.Token != "" {
			t.Fatalf("stale credential proof was accepted: %v", err)
		}
		assertPasswordLoginRows(t, repo, user.ID, 0, 0, 0)
		return
	}
	if err != nil || pending.Token == "" {
		t.Fatalf("current factor did not require pending MFA: %v", err)
	}
	assertPasswordLoginRows(t, repo, user.ID, 0, 1, 0)
}

func checkPasswordLoginIssuanceFirst(
	t *testing.T, repo *repository.Repository, mutation string,
) {
	t.Helper()
	user := passwordProofUser(t, repo)
	session, pending, err := completePasswordProof(t, repo, user)
	if err != nil || session.ID == "" || pending.Token != "" {
		t.Fatalf("current proof did not issue a final session: %v", err)
	}
	assertPasswordLoginRows(t, repo, user.ID, 1, 0, 1)

	mutatePasswordLoginUser(t, repo, user.ID, mutation)
	assertPasswordLoginRows(t, repo, user.ID, 0, 0, 1)
}

func checkPasswordLoginPendingRevocation(
	t *testing.T, repo *repository.Repository,
) {
	t.Helper()
	user := passwordProofUser(t, repo)
	mutatePasswordLoginUser(t, repo, user.ID, "factor")
	_, pending, err := completePasswordProof(t, repo, user)
	if err != nil {
		t.Fatal(err)
	}
	challenges := mfa.NewChallengeService(
		mfa.ChallengeServiceConfig{Repository: repo},
	)
	resolved, err := challenges.ResolveLogin(
		t.Context(),
		pending.Token,
		pending.CSRF,
	)
	if err != nil {
		t.Fatalf("issued MFA challenge did not resolve: %v", err)
	}

	mutatePasswordLoginUser(t, repo, user.ID, "password")
	err = challenges.ConsumeWith(t.Context(), resolved.Binding,
		func(context.Context, *db.Queries, db.MfaChallenge) error {
			t.Error("the revoked challenge reached the completion callback")
			return nil
		},
	)
	if !errors.Is(err, mfa.ErrChallengeInvalid) {
		t.Fatalf("consume revoked challenge: %v", err)
	}
	session, pending, err := completePasswordProof(t, repo, user)
	if !errors.Is(err, auth.ErrPasswordChanged) ||
		session.ID != "" || pending.Token != "" {
		t.Fatalf("stale proof for an enrolled account was accepted: %v", err)
	}
	assertPasswordLoginRows(t, repo, user.ID, 0, 0, 0)
}

func passwordProofUser(t *testing.T, repo *repository.Repository) db.User {
	t.Helper()
	user, err := repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: t.Name() + "@example.test", Name: "Password login fixture",
		Role: "viewer", PasswordHash: "fixture proof",
	})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func mutatePasswordLoginUser(
	t *testing.T, repo *repository.Repository, userID int64, mutation string,
) {
	t.Helper()
	if mutation == "password" {
		if err := auth.UpdatePassword(t.Context(), repo, auth.PasswordChange{
			UserID: userID, Password: "updated fixture password",
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := mfa.NewFactorStore(
		mfa.FactorStoreConfig{Repository: repo, Box: box},
	)
	if _, err := store.ActivateTOTP(
		t.Context(), userID, "JBSWY3DPEHPK3PXP", 1,
	); err != nil {
		t.Fatal(err)
	}
}

func completePasswordProof(
	t *testing.T, repo *repository.Repository, user db.User,
) (db.Session, mfa.PendingChallenge, error) {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/login",
		nil,
	)
	return handler.CompletePasswordLoginForTest(
		handler.NewAuthHandler(repo), r, user.ID, user.PasswordHash,
	)
}

func assertPasswordLoginRows(
	t *testing.T, repo *repository.Repository,
	userID int64, sessions, challenges, audits int,
) {
	t.Helper()
	for _, check := range []struct {
		query string
		want  int
	}{
		{"SELECT COUNT(*) FROM sessions WHERE user_id = ?", sessions},
		{"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?", challenges},
		{"SELECT COUNT(*) FROM audit_log WHERE user_id = ? AND action = 'login'", audits},
	} {
		var count int
		if err := repo.DB.QueryRowContext(
			t.Context(), check.query, userID,
		).Scan(&count); err != nil || count != check.want {
			t.Fatalf(
				"%s: count %d, want %d: %v",
				check.query,
				count,
				check.want,
				err,
			)
		}
	}
}
