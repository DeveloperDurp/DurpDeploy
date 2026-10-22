package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDiscordNotifier_LongMessageAttachment(t *testing.T) {
	for name, message := range map[string]string{
		"boundary": strings.Repeat("a", 2000),
		"maximum provenance": "Deployment #1 succeeded\nRepository: https://example.test/" +
			strings.Repeat("r", 2027) + "\nArtifact: " + strings.Repeat("a", 2048) +
			"\nCI run: https://ci.example.test/" + strings.Repeat("c", 2024),
		"unicode": strings.Repeat("界🙂", 1000),
	} {
		t.Run(name, func(t *testing.T) {
			captured := make(chan discordTestMessage, 16)
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got := readDiscordTestMessage(t, r)
					if len(got.content) > 2000 {
						t.Error("Discord content exceeds provider limit")
					}
					captured <- got
					w.WriteHeader(http.StatusNoContent)
				}),
			)
			defer srv.Close()
			notifier := notify.NewDiscordNotifierWithClient(srv.Client())
			skipped, err := notifier.Notify(t.Context(), events.Event{
				Type: events.DeploymentSucceeded, Message: message, DiscordWebhookURL: srv.URL,
			})
			if err != nil || skipped {
				t.Fatalf("skipped=%v err=%v", skipped, err)
			}
			got := <-captured
			if len(captured) != 0 {
				t.Fatal("long notification used multiple requests")
			}
			assertDiscordTestMessage(t, message, got)
		})
	}
}

type discordTestMessage struct{ content, attachment string }

func readDiscordTestMessage(t *testing.T, r *http.Request) discordTestMessage {
	t.Helper()
	var body struct{ Content string }
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		return discordTestMessage{content: body.Content}
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Error(err)
		return discordTestMessage{}
	}
	defer func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			t.Error(err)
		}
	}()
	if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &body); err != nil {
		t.Error(err)
	}
	file, header, err := r.FormFile("files[0]")
	if err != nil {
		t.Error(err)
		return discordTestMessage{}
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	if header.Filename != "deployment-notification.txt" {
		t.Error("missing notification filename")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Error(err)
	}
	return discordTestMessage{content: body.Content, attachment: string(data)}
}

func assertDiscordTestMessage(
	t *testing.T,
	message string,
	got discordTestMessage,
) {
	t.Helper()
	if len(message) <= 2000 {
		if got.content != message || got.attachment != "" {
			t.Fatal("short message changed")
		}
		return
	}
	if got.attachment != message ||
		!strings.Contains(got.content, "deployment_succeeded") {
		t.Fatal("long notification lost its complete body or event summary")
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

	n := notify.NewDiscordNotifierWithClient(srv.Client())
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

	n := notify.NewDiscordNotifierWithClient(srv.Client())
	_, err := n.Notify(context.Background(), events.Event{
		Message:           "x",
		DiscordWebhookURL: srv.URL,
	})
	if err == nil {
		t.Fatal("expected an error for a non-2xx webhook response")
	}
}
