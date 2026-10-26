package notify

import (
	"context"
	"net/http"

	"durpdeploy/internal/events"
)

// DiscordNotifier posts a message to a project's Discord incoming webhook
// (https://discord.com/developers/docs/resources/webhook#execute-webhook).
// It skips (rather than fails) when the project has no webhook configured.
type DiscordNotifier struct {
	httpClient *http.Client
}

func NewDiscordNotifier() *DiscordNotifier {
	return NewDiscordNotifierWithClient(NewHTTPClient())
}

func NewDiscordNotifierWithClient(client *http.Client) *DiscordNotifier {
	return &DiscordNotifier{httpClient: client}
}

func (d *DiscordNotifier) Name() string { return "discord" }

func (d *DiscordNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	if event.DiscordWebhookURL != "" && len(event.Message) > 2000 {
		return false, d.notifyAttachment(ctx, event)
	}
	if event.DiscordWebhookURL == "" {
		return true, nil
	}

	return false, postJSON(ctx, d.httpClient, event.DiscordWebhookURL,
		map[string]string{"content": event.Message}, "discord webhook")
}
