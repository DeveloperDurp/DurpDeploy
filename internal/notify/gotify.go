package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"durpdeploy/internal/events"
)

// GotifyNotifier posts a message to a project's Gotify server via its
// message API (https://gotify.net/docs/pushmsg). It skips (rather than
// fails) when the project has no Gotify URL/token configured.
type GotifyNotifier struct {
	httpClient *http.Client
}

func NewGotifyNotifier() *GotifyNotifier {
	return NewGotifyNotifierWithClient(NewHTTPClient())
}

func NewGotifyNotifierWithClient(client *http.Client) *GotifyNotifier {
	return &GotifyNotifier{httpClient: client}
}

func (g *GotifyNotifier) Name() string { return "gotify" }

func (g *GotifyNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	if event.GotifyURL == "" || event.GotifyToken == "" {
		return true, nil
	}

	u, err := url.Parse(event.GotifyURL)
	if err != nil {
		return false, fmt.Errorf("invalid gotify url: %w", err)
	}
	u.Path, _ = url.JoinPath(u.Path, "/message")
	q := u.Query()
	q.Set("token", event.GotifyToken)
	u.RawQuery = q.Encode()

	return false, postJSON(ctx, g.httpClient, u.String(), map[string]any{
		"title":    "durpdeploy",
		"message":  event.Message,
		"priority": 5,
	}, "gotify")
}
