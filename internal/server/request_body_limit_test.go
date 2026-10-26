package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/handler"
)

type observedRequestBody struct {
	reads int
}

func TestPasswordFormConfiguredAboveImplicitParseLimit(t *testing.T) {
	limits := config.Defaults().Limits.HTTP
	limits.PasswordFormBytes = 11 << 20
	password := strings.Repeat("p", 10<<20)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.PostFormValue("password") != password {
			t.Fatal(
				"configured form was capped by the implicit ParseForm limit",
			)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	router := handler.WithRequestBodyLimits(limits)(passwordFormBodyLimit(next))
	req := httptest.NewRequest(
		"POST",
		"/login",
		strings.NewReader("password="+password),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if !called || rec.Code != http.StatusNoContent {
		t.Fatalf(
			"called=%t status=%d body=%.200s",
			called,
			rec.Code,
			rec.Body.String(),
		)
	}
}

func TestWebRequestBodyRejectsOversizedFormBeforeHandler(t *testing.T) {
	called := false
	h := webRequestBodyLimit(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { called = true },
	))
	req := httptest.NewRequest(
		http.MethodPost, "/settings/package-repository",
		strings.NewReader(strings.Repeat("x", (64<<10)+1)),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called || rec.Code != http.StatusRequestEntityTooLarge ||
		rec.Body.String() != "Request body too large\n" {
		t.Fatalf("called=%v status=%d body=%q", called, rec.Code, rec.Body)
	}
}

func TestWebRequestBodyOversizeClosesHTTP1Connection(t *testing.T) {
	server := httptest.NewServer(webRequestBodyLimit(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {
			t.Error("oversized request reached handler")
		},
	)))
	t.Cleanup(server.Close)
	connection, err := net.DialTimeout(
		"tcp",
		server.Listener.Addr().String(),
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// Send only headers. The known oversized length must be rejected without
	// reading a body, and the unread upload must not enter a keepalive request.
	_, err = fmt.Fprintf(
		connection,
		"POST /projects HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n",
		handler.MaxFormBodyBytes+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 413 ||
		string(body) != "Request body too large\n" ||
		!response.Close {
		t.Fatalf(
			"status=%d close=%v body=%q",
			response.StatusCode,
			response.Close,
			body,
		)
	}
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
