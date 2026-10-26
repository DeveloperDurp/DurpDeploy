// Package notify provides events.Notifier implementations that deliver
// deployment lifecycle events to Slack (webhook), email (SMTP), and
// Gotify (self-hosted push).
package notify

import (
	"context"
	"net/http"

	"durpdeploy/internal/events"
)

// SlackNotifier posts a plain-text message to a project's incoming webhook
// URL. It skips (rather than fails) when the project has no webhook
// configured.
type SlackNotifier struct {
	httpClient *http.Client
}

func NewSlackNotifier() *SlackNotifier {
	return NewSlackNotifierWithClient(NewHTTPClient())
}

func NewSlackNotifierWithClient(client *http.Client) *SlackNotifier {
	return &SlackNotifier{httpClient: client}
}

func (s *SlackNotifier) Name() string { return "slack" }

func (s *SlackNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	if event.SlackWebhookURL == "" {
		return true, nil
	}

	return false, postJSON(ctx, s.httpClient, event.SlackWebhookURL,
		map[string]string{"text": event.Message}, "slack webhook")
}
