package handler

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestInternalErrorMiddlewareSanitizesWebResponseAndLogsDetail(
	t *testing.T,
) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	sensitive := errors.New(
		"sql: no such column: secret_ciphertext in /srv/durpdeploy.db",
	)
	h := middleware.RequestID(InternalErrorMiddleware(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, sensitive.Error(), http.StatusInternalServerError)
		},
	)))
	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != internalErrorMessage+"\n" {
		t.Fatalf("body = %q, want generic error", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), sensitive.Error()) {
		t.Fatal("response exposed the internal error")
	}
	for _, want := range []string{
		"sql: no such column",
		"/srv/durpdeploy.db",
		`"request_id":`,
		`"method":"GET"`,
		`"path":"/projects"`,
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q does not contain %q", logs.String(), want)
		}
	}
}

func TestInternalErrorMiddlewareUsesStableAPIContract(t *testing.T) {
	h := InternalErrorMiddleware(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"cipher: invalid ciphertext"}`))
		},
	))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != `{"error":"internal server error"}`+"\n" {
		t.Fatalf("body = %q, want stable JSON error", rec.Body.String())
	}
}

func TestInternalErrorMiddlewarePreservesExpectedErrors(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusUnprocessableEntity,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := InternalErrorMiddleware(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "specific actionable error", status)
				},
			))
			req := httptest.NewRequest(http.MethodPost, "/projects", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != status {
				t.Fatalf("status = %d, want %d", rec.Code, status)
			}
			if rec.Body.String() != "specific actionable error\n" {
				t.Fatalf("body = %q, want specific error", rec.Body.String())
			}
		})
	}
}

func TestInternalErrorMiddlewarePassesSuccessfulWritesThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	h := InternalErrorMiddleware(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("first"))
			if rec.Body.String() != "first" {
				t.Fatal("successful response was buffered")
			}
			_, _ = w.Write([]byte(" second"))
		},
	))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs.txt", nil))

	if rec.Body.String() != "first second" {
		t.Fatalf(
			"body = %q, want streamed successful writes",
			rec.Body.String(),
		)
	}
}

func TestInternalErrorMiddlewarePreservesMarkedSafeServerError(
	t *testing.T,
) {
	h := InternalErrorMiddleware(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			markSafeErrorResponse(w)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("safe password fallback"))
		},
	))
	rec := httptest.NewRecorder()
	h.ServeHTTP(
		rec,
		httptest.NewRequest(http.MethodGet, "/login/oidc", nil),
	)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Body.String() != "safe password fallback" {
		t.Fatalf("body = %q, want safe response", rec.Body.String())
	}
	if rec.Header().Get(safeErrorHeader) != "" {
		t.Fatal("internal safe-error marker leaked to client")
	}
}
