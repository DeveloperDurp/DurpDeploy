package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
)

func TestLogin_EnrolledUserCreatesPendingMFAStateWithoutBrowserSession(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	user := h.seedUser(t, "mfa@example.com", "hunter2")
	if _, err := h.repo.Queries.CreateTOTP(
		context.Background(),
		db.CreateTOTPParams{
			UserID:        user.ID,
			EncryptedSeed: []byte("not-used-by-login"),
			LastAcceptedStep: sql.NullInt64{
				Int64: 1,
				Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("create enrolled factor: %v", err)
	}

	// When
	client := newJar(t)
	resp, err := client.PostForm(h.server+"/login", url.Values{
		"email":    {"mfa@example.com"},
		"password": {"hunter2"},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if location := resp.Header.Get("Location"); location != "/login/mfa" {
		t.Fatalf("location = %q, want pending MFA route", location)
	}
	if cacheControl := resp.Header.Get(
		"Cache-Control",
	); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cacheControl)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "session" {
			t.Fatal("enrolled login set a full browser session cookie")
		}
	}
	var sessions, challenges int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		user.ID,
	).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		user.ID,
	).Scan(&challenges); err != nil {
		t.Fatalf("count MFA challenges: %v", err)
	}
	if sessions != 0 || challenges != 1 {
		t.Fatalf(
			"sessions=%d challenges=%d, want pending challenge only",
			sessions,
			challenges,
		)
	}
	rows, err := h.repo.Queries.ListAuditLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("list audit rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("pending login audit rows = %d, want 0", len(rows))
	}
}
