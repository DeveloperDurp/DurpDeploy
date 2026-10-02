package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type observedRequestBody struct {
	reads int
}

func (b *observedRequestBody) Read([]byte) (int, error) {
	b.reads++
	return 0, io.EOF
}

func TestWebRequestBody_UnauthenticatedRequestsDoNotReadForms(t *testing.T) {
	// Given: protected and unmatched paths with attacker-controlled bodies.
	h := newOIDCRouterHarness(t)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	for _, path := range []string{"/projects", "/missing", "/static/missing"} {
		body := &observedRequestBody{}
		req := httptest.NewRequest("POST", path, io.NopCloser(body))
		req.ContentLength = -1
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		// When
		router.ServeHTTP(rec, req)
		// Then
		if body.reads != 0 || rec.Code < 300 {
			t.Fatalf("path=%s reads=%d status=%d", path, body.reads, rec.Code)
		}
	}
}

func TestWebRequestBody_MFARateLimitPrecedesParsing(t *testing.T) {
	// Given: the public MFA IP budget is exhausted.
	h := newOIDCRouterHarness(t)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(
			"POST",
			"/login/mfa/totp",
			strings.NewReader(""),
		)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	for _, path := range []string{
		"/login/mfa/totp", "/login/mfa/recovery",
		"/login/mfa/webauthn/begin", "/login/mfa/webauthn/finish",
	} {
		body := &observedRequestBody{}
		req := httptest.NewRequest("POST", path, io.NopCloser(body))
		req.ContentLength = -1
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		// When
		router.ServeHTTP(rec, req)
		// Then
		if body.reads != 0 || rec.Code != 429 {
			t.Fatalf("path=%s reads=%d status=%d", path, body.reads, rec.Code)
		}
	}
	// Cancellation is not a factor attempt and stays available after throttling.
	req := httptest.NewRequest("POST", "/login/mfa/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cancel status=%d", rec.Code)
	}
}
