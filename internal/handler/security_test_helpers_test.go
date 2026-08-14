package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

var securityHiddenValue = regexp.MustCompile(
	`name="([a-z_]+)" value="([^"]+)"`,
)

func postSecurityForm(
	t *testing.T,
	h *authHarness,
	session *authedSession,
	path string,
	values url.Values,
	next http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPost,
		path,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: session.sessionToken})
	rec := httptest.NewRecorder()
	auth.AuthMiddleware(h.repo)(next).ServeHTTP(rec, req)
	return rec
}

func postSecurityJSON(
	t *testing.T,
	h *authHarness,
	session *authedSession,
	path string,
	body string,
	next http.HandlerFunc,
	token string,
	challengeCSRF string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MFA-Challenge", token)
	req.Header.Set("X-MFA-Challenge-CSRF", challengeCSRF)
	req.AddCookie(&http.Cookie{Name: "session", Value: session.sessionToken})
	rec := httptest.NewRecorder()
	auth.AuthMiddleware(h.repo)(next).ServeHTTP(rec, req)
	return rec
}

func securityHiddenValues(t *testing.T, body string) url.Values {
	t.Helper()
	values := url.Values{}
	for _, match := range securityHiddenValue.FindAllStringSubmatch(body, -1) {
		values.Set(match[1], match[2])
	}
	if values.Get("challenge_token") == "" ||
		values.Get("challenge_csrf") == "" {
		t.Fatal("security response omitted challenge credentials")
	}
	return values
}

func readSession(t *testing.T, h *authHarness, id string) db.GetSessionRow {
	t.Helper()
	session, err := h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{ID: id},
	)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return session
}

func countSecurityRows(
	t *testing.T,
	h *authHarness,
	query string,
	userID int64,
) int {
	t.Helper()
	var count int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		query,
		userID,
	).Scan(&count); err != nil {
		t.Fatalf("count security rows: %v", err)
	}
	return count
}

func seedSecuritySession(
	t *testing.T,
	h *authHarness,
	user *db.User,
) *authedSession {
	t.Helper()
	token, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("create session credentials: %v", err)
	}
	if _, err := h.repo.Queries.CreateSession(
		context.Background(),
		db.CreateSessionParams{
			ID:        token,
			UserID:    user.ID,
			CsrfToken: csrf,
			ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
		},
	); err != nil {
		t.Fatalf("create security session: %v", err)
	}
	return &authedSession{
		user: user, sessionToken: token, csrfToken: csrf,
	}
}

func securityCredentialParams(
	userID int64,
	id []byte,
) db.CreateWebAuthnCredentialParams {
	return db.CreateWebAuthnCredentialParams{
		CredentialID: id, UserID: userID, Name: "primary", PublicKey: []byte{2},
		TransportsJson: "[]", AttestationType: "none", AttestationFormat: "none",
	}
}
