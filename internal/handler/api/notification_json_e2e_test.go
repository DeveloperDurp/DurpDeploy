//go:build e2e

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/notify"
)

func TestJSONNotificationDeliveryAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	setupJSONNotificationDelivery(t, f)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "delivery check", "script_body": "echo delivery-ok",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "json-delivery"}, 201), &release)
	for _, outcome := range []string{"success", "status", "connection", "timeout"} {
		t.Run(outcome, func(t *testing.T) {
			checkJSONNotificationDelivery(t, f, release.ID, outcome)
		})
	}
}

func setupJSONNotificationDelivery(t *testing.T, f *artifactE2E) {
	t.Helper()
	client := notificationFixtureClient(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Error(err)
				return
			}
			if r.Method != http.MethodPost ||
				r.Header.Get("Content-Type") != "application/json" {
				t.Error("delivery lost POST or JSON content type")
			}
			switch strings.Split(r.URL.Path, "/")[1] {
			case "status":
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, "json-delivery-secret")
			case "connection":
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			case "timeout":
				<-r.Context().Done()
			default:
				w.WriteHeader(http.StatusNoContent)
			}
		}),
	)
	bus := events.NewBus(f.h.repo)
	bus.Register(notify.NewSlackNotifierWithClient(client))
	bus.Register(notify.NewGotifyNotifierWithClient(client))
	bus.Register(notify.NewDiscordNotifierWithClient(client))
	bus.Register(artifactNotifier{f.done})
	f.h.runner.SetEventBus(bus)
}

func notificationFixtureClient(
	t *testing.T,
	handler http.Handler,
) *http.Client {
	t.Helper()
	peer := httptest.NewServer(handler)
	t.Cleanup(peer.Close)
	client := peer.Client()
	// Only the injected fixture client maps public test names to its local peer.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(
			ctx,
			network,
			peer.Listener.Addr().String(),
		)
	}
	client.Transport = transport
	client.Timeout = time.Second
	t.Cleanup(transport.CloseIdleConnections)
	return client
}

func checkJSONNotificationDelivery(
	t *testing.T, f *artifactE2E, releaseID int64, outcome string,
) {
	t.Helper()
	endpoint := "http://notification.example.test/" + outcome
	f.api(t, "PUT", f.base()+"/notifications", map[string]string{
		"slack_webhook_url": endpoint + "/slack/json-delivery-secret",
		"gotify_url":        endpoint + "/gotify", "gotify_token": "json-delivery-secret",
		"discord_webhook_url": endpoint + "/discord/json-delivery-secret",
	}, 200)
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{"release_id": releaseID,
			"environment_id": f.environment.ID}, 201), &deployment)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	awaitNotificationHistory(
		t,
		f,
		fmt.Sprintf("Deployment #%d succeeded", deployment.ID),
	)
	var rows []db.ListNotificationEventsRow
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/admin/notifications", nil, 200),
		&rows,
	)
	want := "failed: delivery failed"
	if outcome == "success" {
		want = "ok"
	}
	checkJSONDeliveryHistory(t, rows, deployment.ID, want)
	stored, err := f.h.repo.Queries.ListNotificationEvents(t.Context(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	checkJSONDeliveryHistory(t, stored, deployment.ID, want)
	body := f.web(t, "GET", "/admin/notifications", nil, 200)
	if !strings.Contains(body, want) ||
		strings.Contains(body, "json-delivery-secret") {
		t.Fatal("web history lost delivery status or exposed credentials")
	}
}

func checkJSONDeliveryHistory(
	t *testing.T,
	rows []db.ListNotificationEventsRow,
	deploymentID int64,
	want string,
) {
	t.Helper()
	count := 0
	for _, row := range rows {
		if row.DeploymentID.Int64 != deploymentID {
			continue
		}
		count++
		var results map[string]string
		if err := json.Unmarshal([]byte(row.Results), &results); err != nil {
			t.Fatal(err)
		}
		for _, channel := range []string{"slack", "gotify", "discord"} {
			if results[channel] != want {
				t.Fatalf(
					"%s result=%q want=%q",
					channel,
					results[channel],
					want,
				)
			}
		}
		if strings.Contains(row.Results, "json-delivery-secret") {
			t.Fatal("history exposed credentials")
		}
	}
	if count != 2 {
		t.Fatalf(
			"history has %d rows for deployment %d, want 2",
			count,
			deploymentID,
		)
	}
}
