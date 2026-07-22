package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/events"
)

// DiscordNotifier posts a message to a project's Discord incoming webhook
// (https://discord.com/developers/docs/resources/webhook#execute-webhook).
// It skips (rather than fails) when the project has no webhook configured.
type DiscordNotifier struct {
	httpClient *http.Client
}

func NewDiscordNotifier() *DiscordNotifier {
	return &DiscordNotifier{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (d *DiscordNotifier) Name() string { return "discord" }

func (d *DiscordNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	if event.DiscordWebhookURL == "" {
		return true, nil
	}

	body, err := json.Marshal(map[string]string{"content": event.Message})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		event.DiscordWebhookURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf(
			"discord webhook returned status %d",
			resp.StatusCode,
		)
	}
	return false, nil
}
