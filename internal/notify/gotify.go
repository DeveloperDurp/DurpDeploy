package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"durpdeploy/internal/events"
)

// GotifyNotifier posts a message to a project's Gotify server via its
// message API (https://gotify.net/docs/pushmsg). It skips (rather than
// fails) when the project has no Gotify URL/token configured.
type GotifyNotifier struct {
	httpClient *http.Client
}

func NewGotifyNotifier() *GotifyNotifier {
	return &GotifyNotifier{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (g *GotifyNotifier) Name() string { return "gotify" }

func (g *GotifyNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	if event.GotifyURL == "" || event.GotifyToken == "" {
		return true, nil
	}

	body, err := json.Marshal(map[string]any{
		"title":    "durpdeploy",
		"message":  event.Message,
		"priority": 5,
	})
	if err != nil {
		return false, err
	}

	u, err := url.Parse(event.GotifyURL)
	if err != nil {
		return false, fmt.Errorf("invalid gotify url: %w", err)
	}
	u.Path, _ = url.JoinPath(u.Path, "/message")
	q := u.Query()
	q.Set("token", event.GotifyToken)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		u.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("gotify returned status %d", resp.StatusCode)
	}
	return false, nil
}
