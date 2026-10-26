package notify_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/events"
	"durpdeploy/internal/notify"
)

type jsonDeliveryTransport func(*http.Request) (*http.Response, error)

func (f jsonDeliveryTransport) RoundTrip(
	r *http.Request,
) (*http.Response, error) {
	return f(r)
}

type jsonDeliveryBody struct {
	io.Reader
	closed bool
}

func (b *jsonDeliveryBody) Close() error { b.closed = true; return nil }

func jsonNotifiers(client *http.Client) map[string]events.Notifier {
	return map[string]events.Notifier{
		"slack":   notify.NewSlackNotifierWithClient(client),
		"gotify":  notify.NewGotifyNotifierWithClient(client),
		"discord": notify.NewDiscordNotifierWithClient(client),
	}
}

func jsonEvent(endpoint string) events.Event {
	return events.Event{
		Message: "delivery", SlackWebhookURL: endpoint,
		GotifyURL: endpoint, GotifyToken: "query-secret",
		DiscordWebhookURL: endpoint,
	}
}

func TestJSONNotifiersResponseCleanup(t *testing.T) {
	for _, code := range []int{200, 204, 299, 300, 503} {
		for _, name := range []string{"slack", "gotify", "discord"} {
			t.Run(fmt.Sprintf("%s/%d", name, code), func(t *testing.T) {
				checkJSONResponse(t, name, code)
			})
		}
	}
}

func checkJSONResponse(t *testing.T, name string, code int) {
	t.Helper()
	body := &jsonDeliveryBody{Reader: strings.NewReader("credential-secret")}
	client := &http.Client{
		Transport: jsonDeliveryTransport(
			func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost ||
					r.Header.Get("Content-Type") != "application/json" {
					t.Error("request lost POST or JSON content type")
				}
				return &http.Response{
					StatusCode: code,
					Body:       body,
					Header:     make(http.Header),
				}, nil
			},
		),
	}
	notifier := jsonNotifiers(client)[name]
	skipped, err := notifier.Notify(
		t.Context(),
		jsonEvent("https://example.test/hook"),
	)
	if skipped || !body.closed {
		t.Fatal("configured delivery skipped or response body left open")
	}
	if code < 300 {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	label := name
	if name != "gotify" {
		label += " webhook"
	}
	if err == nil ||
		err.Error() != fmt.Sprintf("%s returned status %d", label, code) {
		t.Fatalf("status error changed: %v", err)
	}
}

func TestJSONNotifiersConnectionFailure(t *testing.T) {
	want := errors.New("connection failed")
	client := &http.Client{
		Transport: jsonDeliveryTransport(
			func(*http.Request) (*http.Response, error) {
				return nil, want
			},
		),
	}
	for _, notifier := range jsonNotifiers(client) {
		t.Run(notifier.Name(), func(t *testing.T) {
			skipped, err := notifier.Notify(
				t.Context(),
				jsonEvent("https://example.test/hook"),
			)
			if skipped || !errors.Is(err, want) {
				t.Fatalf(
					"connection failure changed: skipped=%t error=%v",
					skipped,
					err,
				)
			}
		})
	}
}

func TestJSONNotifiersTimeout(t *testing.T) {
	peer := httptest.NewServer(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Error(err)
				return
			}
			<-r.Context().Done()
		}),
	)
	defer peer.Close()
	client := peer.Client()
	client.Timeout = 40 * time.Millisecond
	for _, notifier := range jsonNotifiers(client) {
		t.Run(notifier.Name(), func(t *testing.T) {
			skipped, err := notifier.Notify(t.Context(), jsonEvent(peer.URL))
			var timeout net.Error
			if skipped || !errors.As(err, &timeout) || !timeout.Timeout() {
				t.Fatalf(
					"injected client timeout lost: skipped=%t error=%v",
					skipped,
					err,
				)
			}
		})
	}
}

func TestJSONNotifiersRequestCancellation(t *testing.T) {
	client := &http.Client{
		Transport: jsonDeliveryTransport(
			func(r *http.Request) (*http.Response, error) {
				return nil, r.Context().Err()
			},
		),
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, notifier := range jsonNotifiers(client) {
		t.Run(notifier.Name(), func(t *testing.T) {
			skipped, err := notifier.Notify(
				ctx,
				jsonEvent("https://example.test/hook"),
			)
			if skipped || !errors.Is(err, context.Canceled) {
				t.Fatalf(
					"request cancellation lost: skipped=%t error=%v",
					skipped,
					err,
				)
			}
		})
	}
}
