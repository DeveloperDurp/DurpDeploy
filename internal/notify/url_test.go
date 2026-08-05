package notify_test

import (
	"context"
	"net/http"
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
