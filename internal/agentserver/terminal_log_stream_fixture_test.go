package agentserver_test

import (
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func terminalStreamSession(t *testing.T, repo *repository.Repository) string {
	t.Helper()
	user, err := repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: "terminal-stream@example.test", Name: "Terminal stream reader",
		PasswordHash: "unused", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID: session, UserID: user.ID, CsrfToken: csrf,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	return session
}
