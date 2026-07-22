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

func TestDiscordNotifier_SkipsWhenNoWebhook(t *testing.T) {
	n := notify.NewDiscordNotifier()
	skipped, err := n.Notify(context.Background(), events.Event{Message: "hi"})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !skipped {
		t.Fatal("expected skipped=true when no webhook URL is set")
	}
}

func TestDiscordNotifier_PostsMessageToWebhook(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		}),
	)
	defer srv.Close()

	n := notify.NewDiscordNotifier()
	skipped, err := n.Notify(context.Background(), events.Event{
		Message:           "Deployment #1 started",
		DiscordWebhookURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if skipped {
		t.Fatal("expected skipped=false when webhook URL is set")
	}
	if gotBody["content"] != "Deployment #1 started" {
		t.Fatalf("posted content = %q", gotBody["content"])
	}
}

func TestDiscordNotifier_ErrorsOnNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)
	defer srv.Close()

	n := notify.NewDiscordNotifier()
	_, err := n.Notify(context.Background(), events.Event{
		Message:           "x",
		DiscordWebhookURL: srv.URL,
	})
	if err == nil {
		t.Fatal("expected an error for a non-2xx webhook response")
	}
}
