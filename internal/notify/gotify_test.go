package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/events"
	"durpdeploy/internal/notify"
)

func TestGotifyNotifier_SkipsWhenNotConfigured(t *testing.T) {
	n := notify.NewGotifyNotifier()
	skipped, err := n.Notify(context.Background(), events.Event{Message: "hi"})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !skipped {
		t.Fatal("expected skipped=true when no Gotify URL/token is set")
	}
}

func TestGotifyNotifier_SkipsWhenTokenMissing(t *testing.T) {
	n := notify.NewGotifyNotifier()
	skipped, err := n.Notify(context.Background(), events.Event{
		Message:   "hi",
		GotifyURL: "https://gotify.example.com",
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !skipped {
		t.Fatal("expected skipped=true when token is missing")
	}
}

func TestGotifyNotifier_PostsMessage(t *testing.T) {
	var gotBody map[string]any
	var gotPath, gotQuery string
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		}),
	)
	defer srv.Close()

	n := notify.NewGotifyNotifier()
	skipped, err := n.Notify(context.Background(), events.Event{
		Message:     "Deployment #1 started",
		GotifyURL:   srv.URL,
		GotifyToken: "tok123",
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if skipped {
		t.Fatal("expected skipped=false when URL and token are set")
	}
	if gotPath != "/message" {
		t.Fatalf("path = %q, want /message", gotPath)
	}
	if gotQuery != "token=tok123" {
		t.Fatalf("query = %q, want token=tok123", gotQuery)
	}
	if gotBody["message"] != "Deployment #1 started" {
		t.Fatalf("posted message = %q", gotBody["message"])
	}
}

func TestGotifyNotifier_ErrorsOnNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)
	defer srv.Close()

	n := notify.NewGotifyNotifier()
	_, err := n.Notify(context.Background(), events.Event{
		Message:     "x",
		GotifyURL:   srv.URL,
		GotifyToken: "tok",
	})
	if err == nil {
		t.Fatal("expected an error for a non-2xx response")
	}
}
