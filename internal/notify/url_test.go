package notify_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/events"
	"durpdeploy/internal/notify"
)

func TestValidateEndpointURLBlocksLocalTargets(t *testing.T) {
	for _, raw := range []string{
		"http://localhost/hook",
		"http://127.0.0.1/hook",
		"http://10.0.0.1/hook",
		"http://172.16.0.1/hook",
		"http://192.168.1.1/hook",
		"ftp://example.com/hook",
	} {
		t.Run(raw, func(t *testing.T) {
			if err := notify.ValidateEndpointURL(raw); err == nil {
				t.Fatal("expected URL to be rejected")
			}
		})
	}
}

func TestValidateEndpointURLBlocksCGNAT(t *testing.T) {
	t.Run("Given cgnat URL then ValidateEndpointURL", func(t *testing.T) {
		// Given
		rawURL := "http://100.64.0.1/hook"

		// When
		err := notify.ValidateEndpointURL(rawURL)

		// Then
		if err == nil {
			t.Fatalf("expected URL %q to be rejected", rawURL)
		}
		if !strings.Contains(err.Error(), "notification URL host is blocked") {
			t.Fatalf("expected blocked host error, got %v", err)
		}
	})
}

func TestSlackNotifierBlocksResolvedLocalhost(t *testing.T) {
	n := notify.NewSlackNotifier()
	_, err := n.Notify(context.Background(), events.Event{
		Message:         "blocked",
		SlackWebhookURL: "http://localhost/hook",
	})
	if err == nil {
		t.Fatal("expected localhost delivery to be blocked")
	}
}

func TestNotifierWithClientAllowsTestServers(t *testing.T) {
	n := notify.NewSlackNotifierWithClient(http.DefaultClient)
	if n == nil {
		t.Fatal("expected notifier")
	}
}
