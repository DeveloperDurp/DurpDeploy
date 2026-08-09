// Package notify provides events.Notifier implementations that deliver
// deployment lifecycle events to Slack (webhook), email (SMTP), and
// Gotify (self-hosted push).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	body, err := json.Marshal(map[string]string{"text": event.Message})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		event.SlackWebhookURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf(
			"slack webhook returned status %d",
			resp.StatusCode,
		)
	}
	return false, nil
}
