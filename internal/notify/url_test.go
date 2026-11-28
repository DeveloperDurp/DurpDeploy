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
		"http://127.0.0.1./hook",
		"http://LOCALHOST./hook",
		"http://service.localhost./hook",
		"http://host.containers.internal/hook",
		"http://HOST.CONTAINERS.INTERNAL./hook",
		"http://2130706433/hook",
		"http://0x7f.0.0.1/hook",
		"http://0177.0.0.1/hook",
		"http://[::ffff:127.0.0.1]/hook",
		"http://[2001:4860:4860::8888%25eth0]/hook",
	} {
		t.Run(raw, func(t *testing.T) {
			if err := notify.ValidateEndpointURL(raw); err == nil {
				t.Fatal("expected URL to be rejected")
			}
		})
	}
}

func TestValidateEndpointURLAcceptsSupportedDestinations(t *testing.T) {
	for _, raw := range []string{
		"", "https://example.com/hook", "https://EXAMPLE.COM./hook",
		"https://8.8.8.8/hook", "https://[2001:4860:4860::8888]/hook",
		"https://2130706433.example/hook", "https://0x7f.example/hook",
	} {
		if err := notify.ValidateEndpointURL(raw); err != nil {
			t.Errorf("valid endpoint %q: %v", raw, err)
		}
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
